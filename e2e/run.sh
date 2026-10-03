#!/usr/bin/env bash
# Browser tests: builds nothing, uses ../bin/web-ide-pod and the front in ../pod/webdist/dist.
# Each suite gets a fresh pod (temporary data and workspace); suites ending with "+" reuse
# the pod of the previous one (session restore tests). Usage: ./run.sh [suite...]
set -u
cd "$(dirname "$0")"
ROOT=$(cd .. && pwd)
SUITES=("$@")
[ ${#SUITES[@]} -eq 0 ] && SUITES=(editing features restore+ git lsp llm speech perf)
[ -d node_modules/playwright-core ] || npm install --no-audit --no-fund >/dev/null
PORT=${E2E_PORT:-4519}
MODELS=${E2E_MODELS:-$HOME/.cache/web-ide-e2e/models}
TMP=$(mktemp -d)
POD_PID=
stop_pod() {
  [ -n "$POD_PID" ] || return
  kill "$POD_PID" 2>/dev/null
  while kill -0 "$POD_PID" 2>/dev/null; do sleep 0.1; done
  POD_PID=
}
trap 'stop_pod; rm -rf "$TMP"' EXIT
start_pod() {
  rm -rf "$TMP/data" "$TMP/ws"
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
  PATH="$HOME/go/bin:$HOME/sdk/go/bin:$PATH" "$ROOT/bin/web-ide-pod" -addr "127.0.0.1:$PORT" -data "$TMP/data" -workspace "$TMP/ws" \
    -static "$ROOT/pod/webdist/dist" >"$TMP/pod.log" 2>&1 </dev/null &
  POD_PID=$!
  for _ in $(seq 50); do [ -s "$TMP/data/token" ] && curl -s -o /dev/null "http://127.0.0.1:$PORT/" && break; sleep 0.1; done
}
export E2E_URL="http://127.0.0.1:$PORT" E2E_WS="$TMP/ws" E2E_OUT="${E2E_OUT:-$TMP}"
status=0
for s in "${SUITES[@]}"; do
  if [[ $s == *+ ]]; then s=${s%+}; else stop_pod; start_pod; fi
  export E2E_TOKEN=$(cat "$TMP/data/token")
  echo "== $s"
  node "suites/$s.cjs" || status=1
done
[ $status -eq 0 ] && echo "e2e : tout est passé" || { echo "e2e : échecs (journal du pod : $TMP/pod.log)"; cp "$TMP/pod.log" /tmp/web-ide-e2e-pod.log 2>/dev/null; }
exit $status
