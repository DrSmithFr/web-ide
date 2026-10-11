#!/usr/bin/env bash
# Browser tests: builds nothing, uses ../bin/web-ide-pod and the front in ../pod/webdist/dist.
# Each suite gets a fresh pod (temporary data and workspace); suites ending with "+" reuse
# the pod of the previous one (session restore tests). Usage: ./run.sh [suite...]
set -u
cd "$(dirname "$0")"
ROOT=$(cd .. && pwd)
SUITES=("$@")
[ ${#SUITES[@]} -eq 0 ] && SUITES=(editing editor features restore+ keyboard mousenav popups commands git projects explorer lsp llm agent chat plan doodle kanban kanbanai kanbangit docker tunnels preview subagents orchestrator mobile i18n speech perf keeper relay identity)
[ -d node_modules/playwright-core ] || npm install --no-audit --no-fund >/dev/null
PORT=${E2E_PORT:-4519}
RELAY_PORT=${E2E_RELAY_PORT:-4430}
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
  mkdir -p "$TMP/relaydata"
  # TURN on loopback: without -turn-ip the relay advertises the machine IP, and Chromium
  # filters candidates it cannot reach; the suite asserts the pair is a relay one.
  "$ROOT/bin/web-ide-pod" -data "$TMP/relaydata" -addr "127.0.0.1:$RELAY_PORT" -turn-ip 127.0.0.1 relay >"$TMP/relay.log" 2>&1 </dev/null &
  RELAY_PID=$!
  export E2E_RELAY_PID=$RELAY_PID
  for _ in $(seq 50); do curl -s -o /dev/null "http://127.0.0.1:$RELAY_PORT/" && break; sleep 0.1; done
}
trap 'stop_pod; stop_relay; rm -rf "$TMP"' EXIT
start_pod() {
  rm -rf "$TMP/data" "$TMP/ws"
  EXTRA=()
  # The identity suite pairs the pod with the CLI, so its pod must reach the relay too.
  if [ "${1:-}" = relay ] || [ "${1:-}" = identity ]; then EXTRA=(-relay "http://127.0.0.1:$RELAY_PORT" -force-turn); fi
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
  # A git identity for the commits of the tests: CI runners have none. The workspace is in
  # /tmp: another scratch folder keeps the approvals of the changes out of the projects.
  cat >"$TMP/launch-pod.sh" <<EOF
GIT_AUTHOR_NAME=e2e GIT_AUTHOR_EMAIL=e2e@x GIT_COMMITTER_NAME=e2e GIT_COMMITTER_EMAIL=e2e@x \\
WEBIDE_INSTRUCTIONS_HOME="$TMP/home" WEBIDE_SCRATCH="$TMP/scratch" WEBIDE_CLAUDE="$ROOT/e2e/bin/claude" PATH="$ROOT/e2e/bin:$HOME/go/bin:$HOME/sdk/go/bin:$PATH" "$ROOT/bin/web-ide-pod" -addr "127.0.0.1:$PORT" -data "$TMP/data" -workspace "$TMP/ws" \\
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
export E2E_URL="http://127.0.0.1:$PORT" E2E_WS="$TMP/ws" E2E_OUT="${E2E_OUT:-$TMP}" E2E_RESTART_POD="$TMP/restart-pod.sh" E2E_DATA="$TMP/data"
export E2E_RELAY_URL="http://127.0.0.1:$RELAY_PORT" E2E_RELAY_PORT="$RELAY_PORT" E2E_RELAY_DATA="$TMP/relaydata" E2E_BIN="$ROOT/bin/web-ide-pod"
mkdir -p "$E2E_OUT"
status=0
for s in "${SUITES[@]}"; do
  if [[ $s == *+ ]]; then s=${s%+}; else
    # The relay comes up first for the suites that need it: a pod started before the relay
    # fails its first dial and sits in exponential backoff.
    { [ "$s" = relay ] || [ "$s" = identity ]; } && start_relay
    stop_pod; start_pod "$s"
  fi
  export E2E_TOKEN=$(cat "$TMP/data/token")
  # Pod id of the relay client: the pod id + secret live in relay.json of its data folder.
  export E2E_POD_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["podId"])' "$TMP/data/relay.json" 2>/dev/null)
  echo "== $s"
  node "suites/$s.cjs" || status=1
done
[ $status -eq 0 ] && echo "e2e: all passed" || { echo "e2e: failures (pod log: /tmp/web-ide-e2e-pod.log)"; cp "$TMP/pod.log" /tmp/web-ide-e2e-pod.log 2>/dev/null; }
exit $status
