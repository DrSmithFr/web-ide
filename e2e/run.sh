#!/usr/bin/env bash
# Browser tests: builds nothing, uses ../bin/web-ide-pod and the front in ../pod/webdist/dist.
# Each suite gets a fresh pod (temporary data and projects folder); suites ending with "+" reuse
# the pod of the previous one (session restore tests). Usage: ./run.sh [-j N] [suite...]
# -j N (or E2E_JOBS): N workers run the suites in parallel, each one with its own temporary
# folder and block of ports (default: a quarter of the CPUs, 8 at most; -j 1: one by one).
set -u
cd "$(dirname "$0")"
ROOT=$(cd .. && pwd)
SUITES=()
JOBS=${E2E_JOBS:-}
while [ $# -gt 0 ]; do
  case $1 in
    -j) JOBS=$2; shift 2 ;;
    -j*) JOBS=${1#-j}; shift ;;
    *) SUITES+=("$1"); shift ;;
  esac
done
# The longest first: the workers end together.
[ ${#SUITES[@]} -eq 0 ] && SUITES=(kanban milestones chat agents kanbanagents questions features restore+ docker lan identity multipod sync push keeper doodle speech relay quota renew preview git editor agent llm subagents orchestrator kanbanai editing lsp mousenav kanbangit commands mcptools plugins projects popups desks keyboard mobile forwards explorer plan i18n help perf)
[ -d node_modules/playwright-core ] || npm install --no-audit --no-fund >/dev/null
if [ -z "$JOBS" ]; then JOBS=$(($(nproc) / 4)); [ "$JOBS" -gt 8 ] && JOBS=8; [ "$JOBS" -lt 1 ] && JOBS=1; fi
# wait -n -p needs bash 5.1.
((BASH_VERSINFO[0] * 100 + BASH_VERSINFO[1] >= 501)) || JOBS=1

# Parallel: the suites are cut into chains (a suite and the "+" ones following it), each chain
# runs as ./run.sh -j 1 in a worker; perf runs alone at the end, its measures need a quiet machine.
if [ "$JOBS" -gt 1 ]; then
  CHAINS=() SOLO=()
  for s in "${SUITES[@]}"; do
    if [[ $s == *+ ]] && [ ${#CHAINS[@]} -gt 0 ]; then CHAINS[-1]+=" $s"
    elif [ "$s" = perf ]; then SOLO+=("$s")
    else CHAINS+=("$s"); fi
  done
fi
if [ "$JOBS" -gt 1 ] && [ $((${#CHAINS[@]} + ${#SOLO[@]})) -gt 1 ]; then
  OUTS=$(mktemp -d)
  # Ports of worker k: E2E_PORT_BASE + 100k (pod), +1 (relay), +2 (TURN), +3 (fake push
  # service of the push suite), +20 (LAN; the lan
  # suite takes up to +60 for its other pods). Below the ephemeral range of the listen(0).
  BASE=${E2E_PORT_BASE:-21000}
  declare -A SLOT NAME START
  FREE=($(seq 0 $((JOBS - 1))))
  FAILED=()
  T0=$SECONDS
  # Each worker in its own process group: an interrupt stops its pods, browsers and suites.
  set -m
  stop_all() { for p in "${!SLOT[@]}"; do kill -TERM -- "-$p" 2>/dev/null; done; wait; rm -rf "$OUTS"; exit 130; }
  trap stop_all INT TERM
  launch() {
    local slot=${FREE[0]} name=${1%% *} port
    FREE=("${FREE[@]:1}")
    port=$((BASE + 100 * slot))
    E2E_PORT=$port E2E_RELAY_PORT=$((port + 1)) E2E_TURN_PORT=$((port + 2)) E2E_PUSH_PORT=$((port + 3)) E2E_LAN_PORT=$((port + 20)) \
      E2E_POD_LOG="/tmp/web-ide-e2e-$name.pod.log" ./run.sh -j 1 $1 >"$OUTS/$name.log" 2>&1 </dev/null &
    SLOT[$!]=$slot NAME[$!]=$1 START[$!]=$SECONDS
  }
  reap() {
    local pid st
    wait -n -p pid; st=$?
    FREE+=("${SLOT[$pid]}")
    if [ $st -eq 0 ]; then printf '  ok    %3ds  %s\n' $((SECONDS - START[$pid])) "${NAME[$pid]}"
    else printf '  FAIL  %3ds  %s\n' $((SECONDS - START[$pid])) "${NAME[$pid]}"; FAILED+=("${NAME[$pid]%% *}"); fi
    unset "SLOT[$pid]" "NAME[$pid]" "START[$pid]"
  }
  echo "e2e: ${#CHAINS[@]} chains on $JOBS workers${SOLO[*]:+, then ${SOLO[*]} alone}"
  for c in "${CHAINS[@]}"; do
    [ ${#FREE[@]} -eq 0 ] && reap
    launch "$c"
  done
  while [ ${#SLOT[@]} -gt 0 ]; do reap; done
  for c in ${SOLO[@]+"${SOLO[@]}"}; do launch "$c"; reap; done
  for f in ${FAILED[@]+"${FAILED[@]}"}; do
    echo; echo "======== $f"; cat "$OUTS/$f.log"
  done
  rm -rf "$OUTS"
  echo "e2e: $((SECONDS - T0))s"
  [ ${#FAILED[@]} -eq 0 ] && { echo "e2e: all passed"; exit 0; }
  echo "e2e: failures: ${FAILED[*]}"; exit 1
fi

PORT=${E2E_PORT:-4519}
RELAY_PORT=${E2E_RELAY_PORT:-4430}
LAN_PORT=${E2E_LAN_PORT:-4520}
# Not the default 3478: the Go tests of the relay bind that one, and may run meanwhile.
TURN_PORT=${E2E_TURN_PORT:-3479}
PUSH_PORT=${E2E_PUSH_PORT:-4432}
MODELS=${E2E_MODELS:-$HOME/.cache/web-ide-e2e/models}
TMP=$(mktemp -d)
KEEPER_PID=
RELAY_PID=
stop_pod() {
  # The pod may have been restarted by its suite (keeper): its pid is in pod.pid.
  local pid
  pid=$(cat "$TMP/pod.pid" 2>/dev/null) && rm -f "$TMP/pod.pid" && {
    kill "$pid" 2>/dev/null
    while kill -0 "$pid" 2>/dev/null; do sleep 0.1; done
  }
  [ -n "$KEEPER_PID" ] || return 0
  kill "$KEEPER_PID" 2>/dev/null
  while kill -0 "$KEEPER_PID" 2>/dev/null; do sleep 0.1; done
  KEEPER_PID=
}
stop_relay() {
  [ -n "$RELAY_PID" ] || return
  kill "$RELAY_PID" 2>/dev/null
  while kill -0 "$RELAY_PID" 2>/dev/null; do sleep 0.1; done
  RELAY_PID=
}
# The relay of the relay suite (its own pid, never a pkill: the pod running the assistant
# has the same name).
start_relay() {
  # A fresh relay per suite: a relay kept from the previous suite still holds the pod record
  # of that run, and a pod with another secret is refused by it.
  rm -rf "$TMP/relaydata"
  mkdir -p "$TMP/relaydata"
  # TURN on loopback: without -turn-ip the relay advertises the machine IP, and Chromium
  # filters candidates it cannot reach; the suite asserts the pair is a relay one.
  # The push suite runs a fake push service: the relay lets its endpoints through.
  # The quota suite gives the relay a monthly TURN quota (E2E_RELAY_QUOTA), and E2E_TURN_TTL
  # shortens its credentials.
  # A fake OAuth App and webhook secret: the identity suite links GitHub up to the redirect to
  # github.com, and makes the account a supporter with a signed sponsorship event. The relay
  # looks for the VIPs its CLI writes every second (WEBIDE_LEVEL_POLL), not every 30 s.
  QUOTA=()
  [ -n "${E2E_RELAY_QUOTA:-}" ] && QUOTA=(-quota "$E2E_RELAY_QUOTA")
  WEBIDE_TURN_TTL=${E2E_TURN_TTL:-} WEBIDE_LEVEL_POLL=1s WEBIDE_GITHUB_CLIENT=e2e-client WEBIDE_GITHUB_SECRET=e2e-secret WEBIDE_GITHUB_WEBHOOK=e2e-hook "$ROOT/bin/web-ide-pod" -data "$TMP/relaydata" -addr "127.0.0.1:$RELAY_PORT" -turn-ip 127.0.0.1 -turn-port "$TURN_PORT" \
    ${QUOTA[@]+"${QUOTA[@]}"} -push-test-endpoint "http://127.0.0.1:$PUSH_PORT/push/" relay >"$TMP/relay.log" 2>&1 </dev/null &
  RELAY_PID=$!
  export E2E_RELAY_PID=$RELAY_PID
  for _ in $(seq 50); do curl -s -o /dev/null "http://127.0.0.1:$RELAY_PORT/" && break; sleep 0.1; done
}
trap 'stop_pod; stop_relay; rm -rf "$TMP"' EXIT
start_pod() {
  rm -rf "$TMP/data" "$TMP/ws"
  EXTRA=()
  # The identity, sync and push suites pair the pod with the CLI, so its pod must reach the relay too.
  if [ "${1:-}" = relay ] || [ "${1:-}" = identity ] || [ "${1:-}" = sync ] || [ "${1:-}" = push ] || [ "${1:-}" = multipod ] || [ "${1:-}" = quota ] || [ "${1:-}" = renew ]; then EXTRA=(-relay "http://127.0.0.1:$RELAY_PORT" -force-turn); fi
  # The LAN link only in its suite, on loopback and with the addresses typed (no mDNS): a
  # paired pod of another suite must not take the port of the IDE's own pod.
  if [ "${1:-}" = lan ]; then EXTRA+=(-lan "127.0.0.1:$LAN_PORT" -mdns=false -name alpha)
  # The multipod suite tests *Update this pod*: alpha stands for a release, no pod installs the
  # development version a build of the source tree reports.
  elif [ "${1:-}" = multipod ]; then EXTRA+=(-lan "127.0.0.1:$LAN_PORT" -mdns=false -name alpha -preferred -pod-version v1.11.0)
  else EXTRA+=(-lan off); fi
  mkdir -p "$TMP/ws" "$TMP/data" "$MODELS"
  # Speech models are kept between runs (downloaded once from Hugging Face).
  ln -s "$MODELS" "$TMP/data/models"
  cp -r fixtures/. "$TMP/ws/"
  python3 - "$TMP/ws/demo/app.db" <<'PY'
import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.executescript('''CREATE TABLE users(id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT);
CREATE INDEX users_email ON users(email);
CREATE TABLE orders(id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), total REAL);''')
c.executemany('INSERT INTO users(name, email) VALUES (?, ?)', [('user%d' % i, 'u%d@x.org' % i) for i in range(250)])
c.commit()
PY
  # Global instructions and skills of the assistant come from the fixtures, not ~/.claude.
  rm -rf "$TMP/home" && cp -r home "$TMP/home"
  # The suite keeper runs the terminals in a keeper, and restarts the pod (E2E_RESTART_POD).
  if [ "$1" = keeper ]; then
    "$ROOT/bin/web-ide-pod" keeper -data "$TMP/data" >"$TMP/keeper.log" 2>&1 </dev/null &
    KEEPER_PID=$!
    for _ in $(seq 50); do [ -S "$TMP/data/keeper.sock" ] && break; sleep 0.1; done
  fi
  # A git identity for the commits of the tests: CI runners have none. The projects folder is in
  # /tmp: another scratch folder keeps the approvals of the changes out of the projects.
  cat >"$TMP/launch-pod.sh" <<EOF
GIT_AUTHOR_NAME=e2e GIT_AUTHOR_EMAIL=e2e@x GIT_COMMITTER_NAME=e2e GIT_COMMITTER_EMAIL=e2e@x \\
WEBIDE_TEST_PLUGINS=1 WEBIDE_INSTRUCTIONS_HOME="$TMP/home" WEBIDE_SCRATCH="$TMP/scratch" WEBIDE_CLAUDE="$ROOT/e2e/bin/claude" PATH="$ROOT/e2e/bin:$HOME/go/bin:$HOME/sdk/go/bin:$PATH" "$ROOT/bin/web-ide-pod" -addr "127.0.0.1:$PORT" -data "$TMP/data" -projects "$TMP/ws" \\
  -static "$ROOT/pod/webdist/dist" ${EXTRA[@]+"${EXTRA[@]}"} >>"$TMP/pod.log" 2>&1 </dev/null &
echo \$! >"$TMP/pod.pid"
for _ in \$(seq 50); do [ -s "$TMP/data/token" ] && curl -s -o /dev/null "http://127.0.0.1:$PORT/" && break; sleep 0.1; done
EOF
  cat >"$TMP/restart-pod.sh" <<EOF
pid=\$(cat "$TMP/pod.pid")
kill -TERM "\$pid"
while kill -0 "\$pid" 2>/dev/null; do sleep 0.1; done
bash "$TMP/launch-pod.sh"
EOF
  : >"$TMP/pod.log"
  bash "$TMP/launch-pod.sh"
}
export E2E_URL="http://127.0.0.1:$PORT" E2E_PORT="$PORT" E2E_WS="$TMP/ws" E2E_OUT="${E2E_OUT:-$TMP}" E2E_RESTART_POD="$TMP/restart-pod.sh" E2E_DATA="$TMP/data"
export E2E_LAN_PORT="$LAN_PORT" E2E_TURN_PORT="$TURN_PORT" E2E_PUSH_PORT="$PUSH_PORT" E2E_TMP="$TMP"
export E2E_RELAY_URL="http://127.0.0.1:$RELAY_PORT" E2E_RELAY_PORT="$RELAY_PORT" E2E_RELAY_DATA="$TMP/relaydata" E2E_RELAY_LOG="$TMP/relay.log" E2E_BIN="$ROOT/bin/web-ide-pod"
mkdir -p "$E2E_OUT"
status=0
for s in "${SUITES[@]}"; do
  if [[ $s == *+ ]]; then s=${s%+}; else
    stop_relay
    # The relay comes up first for the suites that need it: a pod started before the relay
    # fails its first dial and sits in exponential backoff.
    if [ "$s" = relay ] || [ "$s" = identity ] || [ "$s" = sync ] || [ "$s" = push ] || [ "$s" = multipod ] || [ "$s" = quota ] || [ "$s" = renew ]; then
      # The quota suite runs the relay with a monthly TURN quota (milestone 5).
      if [ "$s" = quota ]; then export E2E_RELAY_QUOTA=512KB; else unset E2E_RELAY_QUOTA; fi
      # The renew and quota suites shorten the TURN credentials: a tunnel renews them (#86).
      if [ "$s" = renew ] || [ "$s" = quota ]; then export E2E_TURN_TTL=10s; else unset E2E_TURN_TTL; fi
      start_relay
    fi
    stop_pod; start_pod "$s"
  fi
  export E2E_TOKEN=$(cat "$TMP/data/token")
  # Pod id of the relay client: the pod id + secret live in relay.json of its data folder.
  export E2E_POD_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["podId"])' "$TMP/data/relay.json" 2>/dev/null)
  echo "== $s"
  node "suites/$s.cjs" || status=1
done
POD_LOG=${E2E_POD_LOG:-/tmp/web-ide-e2e-pod.log}
[ $status -eq 0 ] && echo "e2e: all passed" || { echo "e2e: failures (pod log: $POD_LOG)"; cp "$TMP/pod.log" "$POD_LOG" 2>/dev/null; }
exit $status
