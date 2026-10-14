#!/bin/sh
# Deploys the relay of a host (antennae.space): the binary goes to /usr/local/bin/web-ide-pod,
# the systemd service is restarted, and the page served is checked against this build.
#
#   deploy-relay.sh --binary <file>   a local build (make deploy-relay)
#   deploy-relay.sh v1.11.0           a published release (its sha256 is checked)
#   deploy-relay.sh --rollback        back to the previous binary on the host
#   --host <user@host>                the relay host (RELAY_HOST)
#   --unit <service>                  the service to restart (RELAY_UNIT)
#   --url <https://…>                 what it serves, for the check (RELAY_URL)
#
# The data of the relay (its SQLite base, its VAPID key, its TLS certificate) lives on the host
# and is never touched. The binary replaced is kept as web-ide-pod.prev, for --rollback.
set -eu

REPO=DrSmithFr/web-ide
HOST=${RELAY_HOST:-john@5.196.7.104}
UNIT=${RELAY_UNIT:-antennae-relay}
URL=${RELAY_URL:-https://antennae.space}
BIN=/usr/local/bin/web-ide-pod
VERSION=
BINARY=
ROLLBACK=

while [ $# -gt 0 ]; do
  case $1 in
    --host) HOST=$2; shift 2 ;;
    --unit) UNIT=$2; shift 2 ;;
    --url) URL=$2; shift 2 ;;
    --binary) BINARY=$2; shift 2 ;;
    --rollback) ROLLBACK=1; shift ;;
    -h|--help) sed -n '2,14p' "$0"; exit 0 ;;
    v*) VERSION=$1; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

# sha256sum on Linux, shasum on macOS.
sha() {
  if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

if [ -n "$ROLLBACK" ]; then
  echo "Rolling back $UNIT on $HOST"
  ssh "$HOST" "set -e
    sudo test -f $BIN.prev
    sudo mv -f $BIN.prev $BIN
    sudo systemctl restart $UNIT
    sleep 2
    systemctl is-active $UNIT
    $BIN -version"
  exit 0
fi

# A published release, with its checksum: what the users install with scripts/install.sh.
if [ -z "$BINARY" ]; then
  [ -n "$VERSION" ] || { echo "no --binary and no version: make deploy-relay builds the current tree" >&2; exit 2; }
  TMP=$(mktemp -d)
  trap 'rm -rf "$TMP"' EXIT
  BASE=https://github.com/$REPO/releases/download/$VERSION
  echo "Downloading $BASE"
  # The name of the archive is the one SHA256SUMS lists: the checksum is checked where it is.
  curl -fsSL -o "$TMP/web-ide-pod-linux-amd64.tar.gz" "$BASE/web-ide-pod-linux-amd64.tar.gz"
  curl -fsSL -o "$TMP/SHA256SUMS" "$BASE/SHA256SUMS"
  grep 'linux-amd64' "$TMP/SHA256SUMS" >"$TMP/one"
  (cd "$TMP" && sha -c one)
  tar -xzf "$TMP/web-ide-pod-linux-amd64.tar.gz" -C "$TMP"
  BINARY=$TMP/web-ide-pod
fi
[ -f "$BINARY" ] || { echo "no such binary: $BINARY" >&2; exit 2; }

# The host runs the new file before it replaces the one it serves: a binary built for another
# system, or one that cannot even print its version, never stops the relay.
echo "Installing on $HOST"
scp -q "$BINARY" "$HOST:/tmp/web-ide-pod.new"
ssh "$HOST" "set -e
  sudo -n true 2>/dev/null || { echo 'sudo needs a password: run with ssh -t' >&2; exit 1; }
  /tmp/web-ide-pod.new -version
  sudo cp -a $BIN $BIN.prev
  sudo install -m755 /tmp/web-ide-pod.new $BIN
  rm -f /tmp/web-ide-pod.new
  sudo systemctl restart $UNIT
  sleep 2
  systemctl is-active $UNIT
  $BIN -version"

# A development build is worth testing on the relay, but no pod can be updated to it: *Update
# this pod* only offers a release tag (identity.IsReleaseTag).
if ! ssh "$HOST" "$BIN -version" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "warning: not a release version: no pod can self-update to this build"
fi

# The Service Worker of the relay is what caches the assets of the pods: the page must serve the
# one of this build, not an older one held by a proxy.
SW=pod/internal/relay/web/sw.js
if [ -f "$SW" ] && curl -fsSL --max-time 15 -o /tmp/relay-sw.js "$URL/sw.js" 2>/dev/null; then
  want=$(sha "$SW" | cut -d' ' -f1)
  got=$(sha /tmp/relay-sw.js | cut -d' ' -f1)
  rm -f /tmp/relay-sw.js
  if [ "$want" = "$got" ]; then
    echo "$URL serves the Service Worker of this build"
  else
    echo "warning: $URL serves another Service Worker (another build, or a proxy that caches it)" >&2
  fi
fi

echo "Relay: $URL · service $UNIT on $HOST · previous binary kept as $BIN.prev (--rollback)"
