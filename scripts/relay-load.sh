#!/bin/sh
# A second relay on the relay host, for load tests (#72): its own service, its own data folder,
# its own ports and its own TURN port range. The hosted relay is never touched — a run that runs
# out of relay ports, out of memory or out of uplink runs into this one.
#
#   relay-load.sh up        install the binary, write the unit, open the firewall, start it
#   relay-load.sh down      stop it, remove the unit and its firewall rules (the data is kept)
#   relay-load.sh status    what it reports about itself
#
# The binary comes from --binary (make load-relay builds it for the host). The unit runs the relay
# with a narrow TURN port range — 500 relay ports, so 500 relayed tunnels — which is what makes
# the wall reachable in a test instead of guessed. No -quota, no -domain, no GitHub link: a test
# relay issues its own self-signed certificate, and relayload -insecure accepts it.
set -eu

HOST=${RELAY_HOST:-john@5.196.7.104}
UNIT=${RELAY_TEST_UNIT:-antennae-relay-test}
DATA=${RELAY_TEST_DATA:-/var/lib/antennae-test}
BIN=/usr/local/bin/web-ide-pod-load
USER=${RELAY_USER:-antennae}
HTTP_PORT=${RELAY_TEST_HTTP_PORT:-4431}
TLS_PORT=${RELAY_TEST_TLS_PORT:-4443}
TURN_PORT=${RELAY_TEST_TURN_PORT:-3479}
TURN_RANGE=${RELAY_TEST_TURN_PORTS:-50000-50499}
BINARY=

while [ $# -gt 0 ]; do
  case $1 in
    --host) HOST=$2; shift 2 ;;
    --unit) UNIT=$2; shift 2 ;;
    --binary) BINARY=$2; shift 2 ;;
    up | down | status) CMD=$1; shift ;;
    -h|--help) sed -n '2,16p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
CMD=${CMD:-up}

# The range as ufw names it: two numbers.
TURN_FIRST=$(echo "$TURN_RANGE" | cut -d- -f1)
TURN_LAST=$(echo "$TURN_RANGE" | cut -d- -f2)

case $CMD in
up)
  [ -n "$BINARY" ] && [ -f "$BINARY" ] || { echo "no --binary: make load-relay builds it" >&2; exit 2; }
  echo "Installing the test relay on $HOST (unit $UNIT, data $DATA)"
  scp -q "$BINARY" "$HOST:/tmp/web-ide-pod-load.new"
  ssh "$HOST" "sudo bash -s" <<LOAD
set -e
/tmp/web-ide-pod-load.new -version >/dev/null
install -m755 /tmp/web-ide-pod-load.new $BIN
rm -f /tmp/web-ide-pod-load.new
id -u $USER >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin $USER
install -d -m750 -o $USER -g $USER $DATA
cat > /etc/systemd/system/$UNIT.service <<UNIT
[Unit]
Description=Web IDE relay, load test ($UNIT)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$BIN -data $DATA -addr 0.0.0.0:$HTTP_PORT -tls-port $TLS_PORT \\
  -turn-port $TURN_PORT -turn-ports $TURN_RANGE relay
User=$USER
Group=$USER
StateDirectory=$(basename $DATA)
ReadWritePaths=$DATA
Restart=always
RestartSec=3
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
# The same memory ceiling as the hosted relay: a test that breaks it says what the hosted one
# would do at the same load.
MemoryMax=1G

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable $UNIT
# restart, not --now: a unit whose ExecStart changed must not keep the process it already has.
systemctl restart $UNIT
sleep 2
systemctl is-active $UNIT
# The firewall of the test range, named the way ufw names a range.
if command -v ufw >/dev/null; then
  for rule in "$TLS_PORT/tcp" "$TURN_PORT/tcp" "$TURN_PORT/udp"; do
    ufw status | grep -q "^\$rule " || ufw allow "\$rule" >/dev/null
  done
  # A range is added without --force: ufw rejects --force together with "first:last/udp".
  ufw status | grep -q "^$TURN_FIRST:$TURN_LAST/udp " || ufw allow "$TURN_FIRST:$TURN_LAST/udp" >/dev/null
fi
LOAD
  echo "Test relay up on $HOST: http://${HOST#*@}:$HTTP_PORT · https://${HOST#*@}:$TLS_PORT · TURN $TURN_PORT, relay ports $TURN_RANGE"
  ;;

down)
  echo "Stopping the test relay on $HOST"
  ssh "$HOST" "sudo bash -s" <<UNLOAD
set -e
systemctl stop $UNIT 2>/dev/null || true
systemctl disable $UNIT 2>/dev/null || true
rm -f /etc/systemd/system/$UNIT.service
systemctl daemon-reload
if command -v ufw >/dev/null; then
  for rule in "$TLS_PORT/tcp" "$TURN_PORT/tcp" "$TURN_PORT/udp"; do
    ufw status | grep -q "^\$rule " && ufw delete "\$rule" >/dev/null || true
  done
  # ufw only deletes a rule by its number, the highest first so the numbers stay valid.
  for n in $(ufw status numbered | grep "$TURN_FIRST:$TURN_LAST/udp" | sed -n 's/^\[ *\([0-9]*\)\].*/\1/p' | sort -rn); do
    ufw --force delete "$n"
  done
fi
UNLOAD
  echo "Test relay down (its data is kept in $DATA)"
  ;;

status)
  # The HTTP port is the one bound on every address; the TLS side is bound on the advertised IP.
  ssh "$HOST" "curl -s --max-time 10 http://127.0.0.1:$HTTP_PORT/_relay/status; echo; systemctl is-active $UNIT 2>/dev/null || true"
  ;;

*)
  echo "unknown command: $CMD (up, down, status)" >&2
  exit 2
  ;;
esac
