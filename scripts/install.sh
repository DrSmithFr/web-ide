#!/bin/sh
# Installs the pod as a systemd user service that starts at boot (Linux).
#
#   install.sh                     latest release from GitHub
#   install.sh v1.0.0              a given release
#   install.sh --binary <file>     a local build (make service)
#   install.sh ... --tailscale     also serve the pod over HTTPS on the tailnet
#
# The binary goes to ~/.local/bin/web-ide-pod, the unit to ~/.config/systemd/user/web-ide-pod.service.
# Running it again upgrades the pod; data in ~/.web-ide is kept.
# A second service, web-ide-keeper (a copy of the binary in ~/.local/lib/web-ide), runs the
# terminals: they survive the updates of the pod. It is replaced only when its protocol changes,
# and then updates itself in place (re-exec, same process): its terminals stay.
set -eu

REPO=DrSmithFr/web-ide
ADDR=${WEBIDE_ADDR:-127.0.0.1:4433}
VERSION=latest
BINARY=
TAILSCALE=

while [ $# -gt 0 ]; do
  case $1 in
    --binary) BINARY=$2; shift 2 ;;
    --tailscale) TAILSCALE=1; shift ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    v*) VERSION=$1; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

[ "$(uname -s)" = Linux ] || { echo "The service needs Linux and systemd. On macOS, run the binary directly." >&2; exit 1; }
command -v systemctl >/dev/null || { echo "systemctl not found: the service needs systemd." >&2; exit 1; }

BIN_DIR=$HOME/.local/bin
UNIT_DIR=$HOME/.config/systemd/user
mkdir -p "$BIN_DIR" "$UNIT_DIR"

if [ -z "$BINARY" ]; then
  case $(uname -m) in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "no release build for $(uname -m): build from the sources (make service)" >&2; exit 1 ;;
  esac
  if [ "$VERSION" = latest ]; then
    URL=https://github.com/$REPO/releases/latest/download/web-ide-pod-linux-$ARCH.tar.gz
  else
    URL=https://github.com/$REPO/releases/download/$VERSION/web-ide-pod-linux-$ARCH.tar.gz
  fi
  TMP=$(mktemp -d)
  trap 'rm -rf "$TMP"' EXIT
  echo "Downloading $URL"
  curl -fsSL "$URL" | tar -xz -C "$TMP"
  BINARY=$TMP/web-ide-pod
fi

# Replace through a new file: the running binary may be in use.
install -m755 "$BINARY" "$BIN_DIR/web-ide-pod.new"
mv -f "$BIN_DIR/web-ide-pod.new" "$BIN_DIR/web-ide-pod"
echo "Installed $("$BIN_DIR/web-ide-pod" -version) in $BIN_DIR"

# The keeper: replaced only when the pod speaks another protocol; never overwritten in place,
# it may be running (it re-executes the new file below).
KEEPER_DIR=$HOME/.local/lib/web-ide
KEEPER=$KEEPER_DIR/web-ide-keeper
mkdir -p "$KEEPER_DIR"
NEW_PROTOCOL=$("$BIN_DIR/web-ide-pod" keeper -protocol)
OLD_PROTOCOL=$("$KEEPER" keeper -protocol 2>/dev/null || echo none)
KEEPER_CHANGED=
if [ "$NEW_PROTOCOL" != "$OLD_PROTOCOL" ]; then
  install -m755 "$BIN_DIR/web-ide-pod" "$KEEPER.new"
  mv -f "$KEEPER.new" "$KEEPER"
  KEEPER_CHANGED=1
  echo "Installed the keeper (protocol $NEW_PROTOCOL) in $KEEPER_DIR"
fi

# systemd does not read the shell profile: the PATH of the installing shell is kept so the
# pod finds git, the language servers (gopls also needs go), node, docker…
SVC_PATH=$BIN_DIR:$HOME/go/bin:$HOME/sdk/go/bin:$PATH
cat >"$UNIT_DIR/web-ide-keeper.service" <<EOF
# Web IDE keeper (written by scripts/install.sh): runs the terminals of the pod, so that they
# survive its updates. Stopping it ends the terminals.
[Unit]
Description=Web IDE keeper (terminals of the pod)

[Service]
ExecStart=$KEEPER keeper
Environment=PATH=$SVC_PATH
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
EOF
cat >"$UNIT_DIR/web-ide-pod.service" <<EOF
# Web IDE pod (written by scripts/install.sh): starts at boot, restarts on failure.
[Unit]
Description=Web IDE pod
After=network-online.target web-ide-keeper.service
Wants=web-ide-keeper.service

[Service]
ExecStart=$BIN_DIR/web-ide-pod -addr $ADDR
Environment=PATH=$SVC_PATH
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
EOF

# Lingering starts the user services at boot, without an open session.
if [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" != yes ]; then
  loginctl enable-linger "$(id -un)" || echo "warning: loginctl enable-linger failed; the pod will start with your session only" >&2
fi
systemctl --user daemon-reload
systemctl --user enable web-ide-keeper web-ide-pod >/dev/null 2>&1
if [ -n "$KEEPER_CHANGED" ] && systemctl --user is-active --quiet web-ide-keeper; then
  # The pod stops first (its conversations are taken back by the new one), then the keeper
  # re-executes the new binary, waiting for the answers being written. A keeper that cannot
  # update itself (older than this mechanism) is restarted.
  systemctl --user stop web-ide-pod
  if ! "$KEEPER" keeper -upgrade; then
    echo "warning: the keeper cannot update itself: it restarts, the terminals are closed" >&2
    systemctl --user restart web-ide-keeper
  fi
else
  systemctl --user start web-ide-keeper
fi
systemctl --user restart web-ide-pod

PORT=${ADDR##*:}
if [ -n "$TAILSCALE" ]; then
  # HTTPS with a certificate of the tailnet: the microphone and the clipboard need a secure context.
  tailscale serve --bg "$PORT" || echo "warning: tailscale serve failed (try: sudo tailscale set --operator=$(id -un))" >&2
fi

TOKEN_FILE=$HOME/.web-ide/token
for _ in 1 2 3 4 5 6 7 8 9 10; do [ -s "$TOKEN_FILE" ] && break; sleep 0.3; done
echo
echo "The pod runs: systemctl --user status web-ide-pod web-ide-keeper"
[ -s "$TOKEN_FILE" ] && echo "Open: http://$ADDR/?token=$(cat "$TOKEN_FILE")"
if [ -n "$TAILSCALE" ]; then
  HOST=$(tailscale status --self --json 2>/dev/null | sed -n 's/.*"DNSName": *"\([^"]*\)\.".*/\1/p' | head -n1)
  [ -n "$HOST" ] && [ -s "$TOKEN_FILE" ] && echo "On the tailnet: https://$HOST/?token=$(cat "$TOKEN_FILE")"
fi
