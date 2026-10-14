#!/bin/sh
# Deploys the relay of a host (antennae.space): the binary goes to /usr/local/bin/web-ide-pod,
# the systemd service is restarted, and the page served is checked against this build.
#
#   deploy-relay.sh --binary <file>   a local build (make deploy-relay)
#   deploy-relay.sh v1.11.0           a published release (its sha256 is checked)
#   deploy-relay.sh --rollback        back to the previous binary on the host
#   deploy-relay.sh --setup           bring the host up: service, firewall, backups (idempotent)
#   --host <user@host>                the relay host (RELAY_HOST)
#   --unit <service>                  the service to restart (RELAY_UNIT)
#   --url <https://…>                 what it serves, for the check (RELAY_URL)
#   --env <file>                      KEY=VALUE file sent to /etc/antennae/env (the GitHub keys)
#
# The data of the relay (its SQLite base, its VAPID key, its TLS certificate) lives on the host
# and is never touched. The binary replaced is kept as web-ide-pod.prev, for --rollback.
#
# --setup writes what the hand install of 2026-10-13 had set up: the service user, its data
# folder, the unit with the flags of the hosted relay, the firewall, unattended upgrades, and the
# timer that makes the backups on the host (scripts/relay-watch.sh pulls them off-site). Running
# it again changes nothing.
set -eu

REPO=DrSmithFr/web-ide
HOST=${RELAY_HOST:-john@5.196.7.104}
UNIT=${RELAY_UNIT:-antennae-relay}
URL=${RELAY_URL:-https://antennae.space}
BIN=/usr/local/bin/web-ide-pod
DOMAIN=${RELAY_DOMAIN:-antennae.space}
DATA=${RELAY_DATA:-/var/lib/antennae}
USER=${RELAY_USER:-antennae}
QUOTA=${RELAY_QUOTA:-10GB}
TURN_PORTS=${RELAY_TURN_PORTS:-49160-49999}
PUSH_SUBJECT=${RELAY_PUSH_SUBJECT:-mailto:contact@antennae.space}
ENV_FILE=/etc/antennae/env
VERSION=
BINARY=
ROLLBACK=
SETUP=
ENV_SRC=

while [ $# -gt 0 ]; do
  case $1 in
    --host) HOST=$2; shift 2 ;;
    --unit) UNIT=$2; shift 2 ;;
    --url) URL=$2; shift 2 ;;
    --binary) BINARY=$2; shift 2 ;;
    --env) ENV_SRC=$2; shift 2 ;;
    --rollback) ROLLBACK=1; shift ;;
    --setup) SETUP=1; shift ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
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

# The host itself: user, data folder, unit, firewall, upgrades, backup timer. Idempotent — it
# compares what is there and only writes what differs.
if [ -n "$SETUP" ]; then
  echo "Setting up $HOST (unit $UNIT, domain $DOMAIN, data $DATA)"
  if [ -n "$ENV_SRC" ]; then
    [ -f "$ENV_SRC" ] || { echo "no such env file: $ENV_SRC" >&2; exit 2; }
    scp -q "$ENV_SRC" "$HOST:/tmp/antennae.env.new"
    ssh "$HOST" "set -e
      sudo install -m600 -o root -g $USER /tmp/antennae.env.new $ENV_FILE 2>/dev/null || {
        sudo install -m600 /tmp/antennae.env.new $ENV_FILE; sudo chown root:$USER $ENV_FILE; }
      rm -f /tmp/antennae.env.new"
    echo "$ENV_FILE replaced (the GitHub keys, 0600)"
  fi
  ssh "$HOST" "sudo bash -s" <<SETUP
set -e
unit=/etc/systemd/system/$UNIT.service
# The service user and its data folder (the base, the VAPID key, the ACME cache).
id -u $USER >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin $USER
install -d -m750 -o $USER -g $USER $DATA
[ -f $ENV_FILE ] || { echo '# GITHUB_CLIENT_ID=… GITHUB_CLIENT_SECRET=… GITHUB_TOKEN=… GITHUB_WEBHOOK_SECRET=…' | install -m600 -o root -g $USER $ENV_FILE; }
cat > "\$unit.new" <<UNIT
[Unit]
Description=Web IDE relay ($DOMAIN)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$BIN -data $DATA -addr 0.0.0.0:4430 -tls-port 443 -domain $DOMAIN \\
  -turn-ports $TURN_PORTS -quota $QUOTA -push-subject $PUSH_SUBJECT -github-env $ENV_FILE relay
EnvironmentFile=$ENV_FILE
User=$USER
Group=$USER
Restart=always
RestartSec=3
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
MemoryMax=1G

[Install]
WantedBy=multi-user.target
UNIT
if ! cmp -s "\$unit.new" "\$unit"; then
  mv "\$unit.new" "\$unit"; systemctl daemon-reload; systemctl enable $UNIT
  echo "unit written (the flags of the hosted relay)"
else
  rm -f "\$unit.new"; echo "unit unchanged"
fi
# The firewall: the page, the TURN control port, the range of the TURN relay sockets. The wide
# range of the hand install (32768-60999) goes away.
if command -v ufw >/dev/null; then
  ufw status | grep -q Status:.active || ufw --force enable
  for r in 22/tcp 80/tcp 443/tcp 3478/tcp 3478/udp; do ufw status | grep -q "^\$r " || ufw --force allow \$r; done
  ufw status | grep -q "^$TURN_PORTS/udp " || ufw --force allow $TURN_PORTS/udp
  ufw status | grep -q "^32768-60999/udp " && ufw --force delete 32768:60999/udp || true
  ufw status verbose
fi
# The security updates come by themselves.
apt-get -qq install -y unattended-upgrades >/dev/null
systemctl enable --now unattended-upgrades >/dev/null 2>&1 || true
# The backups of the base, on the host: sqlite3 .backup of relay.db plus the folder, kept under
# $DATA/backups; scripts/relay-watch.sh pulls them off the machine.
cat > /usr/local/bin/antennae-backup.sh <<'BACKUP'
#!/bin/sh
# One backup of the relay: a consistent copy of the base (sqlite3 .backup) and the data folder.
set -eu
data=$1
keep=\${2:-14}
day=\$(date -u +%Y-%m-%d)
mkdir -p "\$data/backups"
if command -v sqlite3 >/dev/null && [ -f "\$data/relay.db" ]; then
  sqlite3 "\$data/relay.db" ".backup '\$data/backups/relay-\$day.db'"
else
  cp -a "\$data/relay.db" "\$data/backups/relay-\$day.db" 2>/dev/null || true
fi
tar -czf "\$data/backups/data-\$day.tar.gz" -C "\$data" --exclude backups .
find "\$data/backups" -type f -mtime +\$keep -delete
chown -R \$(stat -c '%U:%G' "\$data") "\$data/backups"
BACKUP
chmod 755 /usr/local/bin/antennae-backup.sh
cat > /etc/systemd/system/antennae-backup.service <<SERVICE
[Unit]
Description=Backup of the Web IDE relay
[Service]
Type=oneshot
ExecStart=/usr/local/bin/antennae-backup.sh $DATA 14
SERVICE
cat > /etc/systemd/system/antennae-backup.timer <<TIMER
[Unit]
Description=Nightly backup of the Web IDE relay
[Timer]
OnCalendar=03:20
Persistent=true
[Install]
WantedBy=timers.target
TIMER
systemctl daemon-reload
systemctl enable antennae-backup.timer
# certbot and its deploy hook are dropped once autocert has served a certificate of its own: the
# ACME cache of the relay lives in $DATA/acme. /etc/letsencrypt is kept until then.
if [ -d $DATA/acme ] && [ -n "\$(ls -A $DATA/acme 2>/dev/null)" ]; then
  if command -v certbot >/dev/null; then
    rm -f /etc/letsencrypt/renewal-hooks/deploy/antennae.sh
    apt-get -qq purge -y certbot >/dev/null && echo "certbot removed (autocert serves the certificate)"
  fi
else
  echo "autocert has not served a certificate yet: certbot kept"
fi
SETUP
  ssh "$HOST" "systemctl is-active $UNIT" >/dev/null 2>&1 || ssh "$HOST" "sudo systemctl restart $UNIT"
  if [ -z "$BINARY" ] && [ -z "$VERSION" ]; then
    echo "Host set up. Deploy the binary with: make deploy-relay"
    exit 0
  fi
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
