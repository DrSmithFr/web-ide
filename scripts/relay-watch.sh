#!/bin/sh
# The other half of the hosted relay, on a machine of the user (astra-1): the backups pulled off
# the relay host, and the uptime check of the service.
#
#   relay-watch.sh --pull             copy the backups made on the host, keep 30 days
#   relay-watch.sh --status           ask https://antennae.space/_relay/status, say what it answers
#   relay-watch.sh --timers           install the two systemd user timers that run them
#   --host <user@host>                the relay host (RELAY_HOST)
#   --url <https://…>                 the relay page (RELAY_URL)
#   --dir <folder>                    where the backups land (~/.web-ide-relay-backups)
#   --notify <url>                    a POST endpoint told when the relay is down (an ntfy topic)
#
# The relay host makes its own backups (the timer of deploy-relay.sh --setup); this script only
# takes them off the machine, so a lost VPS is not a lost account. Nothing is sent back.
set -eu

HOST=${RELAY_HOST:-john@5.196.7.104}
URL=${RELAY_URL:-https://antennae.space}
DIR=${RELAY_BACKUP_DIR:-$HOME/.web-ide-relay-backups}
DATA=${RELAY_DATA:-/var/lib/antennae}
KEEP=${RELAY_KEEP:-30}
NOTIFY=${RELAY_NOTIFY:-}
PULL=
STATUS=
TIMERS=

while [ $# -gt 0 ]; do
  case $1 in
    --host) HOST=$2; shift 2 ;;
    --url) URL=$2; shift 2 ;;
    --dir) DIR=$2; shift 2 ;;
    --notify) NOTIFY=$2; shift 2 ;;
    --host-dir) DATA=$2; shift 2 ;;
    --pull) PULL=1; shift ;;
    --status) STATUS=1; shift ;;
    --timers) TIMERS=1; shift ;;
    -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

# The backups of the host, read-only over SSH: a day of files, 30 days kept here.
pull() {
  mkdir -p "$DIR"
  if ! scp -q -r "$HOST:$DATA/backups/" "$DIR/" 2>/dev/null; then
    echo "relay-watch: the backups of $HOST could not be read" >&2
    return 1
  fi
  find "$DIR/backups" -type f -mtime +"$KEEP" -delete 2>/dev/null || true
  echo "relay-watch: $(ls "$DIR/backups" 2>/dev/null | wc -l) backup files kept in $DIR/backups"
}

# The anonymous status of the relay: up, version, pods online, accounts seen in 30 days. A page
# answered instead of the JSON means a relay too old for the check, and says so.
status() {
  body=$(curl -fsS --max-time 20 "$URL/_relay/status" 2>/dev/null) || {
    echo "relay-watch: $URL/_relay/status did not answer" >&2
    [ -n "$NOTIFY" ] && curl -fsS -o /dev/null -H "Title: relay down" -d "$URL did not answer its status check" "$NOTIFY" 2>/dev/null || true
    return 1
  }
  case $body in
    *'"up":'*) echo "relay-watch: $body" ;;
    *)
      echo "relay-watch: $URL/_relay/status answered a page, not the status (a relay older than the check)" >&2
      [ -n "$NOTIFY" ] && curl -fsS -o /dev/null -H "Title: relay down" -d "$URL answers no status" "$NOTIFY" 2>/dev/null || true
      return 1
      ;;
  esac
}

# The two timers, as user services of the machine that watches the relay (astra-1).
timers() {
  unitdir=$HOME/.config/systemd/user
  mkdir -p "$unitdir"
  cat > "$unitdir/relay-watch-pull.service" <<SERVICE
[Unit]
Description=Pull the backups of the hosted relay
[Service]
Type=oneshot
ExecStart=$(command -v relay-watch.sh || echo "$0") --pull
SERVICE
  cat > "$unitdir/relay-watch-pull.timer" <<TIMER
[Unit]
Description=Pull the backups of the hosted relay daily
[Timer]
OnCalendar=*-*-* 04:10
Persistent=true
[Install]
WantedBy=timers.target
TIMER
  cat > "$unitdir/relay-watch-status.service" <<SERVICE
[Unit]
Description=Check the hosted relay answers
[Service]
Type=oneshot
ExecStart=$(command -v relay-watch.sh || echo "$0") --status
SERVICE
  cat > "$unitdir/relay-watch-status.timer" <<TIMER
[Unit]
Description=Check the hosted relay every 10 minutes
[Timer]
OnCalendar=*:0/10
Persistent=true
[Install]
WantedBy=timers.target
TIMER
  systemctl --user daemon-reload
  systemctl --user enable --now relay-watch-pull.timer relay-watch-status.timer
  echo "relay-watch: the two timers are on ($(systemctl --user is-active relay-watch-status.timer))"
}

if [ -z "$PULL$STATUS$TIMERS" ]; then sed -n '2,15p' "$0"; exit 2; fi
[ -z "$PULL" ] || pull
[ -z "$STATUS" ] || status
[ -z "$TIMERS" ] || timers
