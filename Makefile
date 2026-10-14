# Web IDE: pod Go (single binary) + front SolidJS embedded in it.
GO ?= $(shell command -v go || echo $(HOME)/sdk/go/bin/go)
BIN := bin/web-ide-pod
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build web pod run dev test e2e shots check clean install service deploy-relay relay-build relayload load-relay load-relay-down

build: web pod

web:
	cd web && npm install --no-audit --no-fund && npm run build

# go:embed needs the folder, even before the first front build.
pod/webdist/dist:
	mkdir -p $@ && [ -e $@/index.html ] || echo '<!doctype html><p>Front end not built: run make web</p>' > $@/index.html

pod: pod/webdist/dist
	cd pod && $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../$(BIN) .

run: build
	./$(BIN)

# Development: the pod serves the API, Vite serves the front with hot reload.
# Both accept other machines (-allow-remote): open http://<host>:5173/?token=… from anywhere,
# the pairing goes through the Vite proxy. The token stays the only protection.
# The dev pod has its own port and data folder, so it never touches the installed one (make
# service); its data starts as a copy of ~/.web-ide (the speech models are shared).
DEV_ADDR ?= 0.0.0.0:4434
DEV_DATA ?= $(HOME)/.web-ide-dev
# The dev pod has its own keeper: restarting the pod alone keeps its terminals.
dev: pod/webdist/dist $(DEV_DATA)
	cd pod && $(GO) build -o ../bin/web-ide-pod-dev . && (../bin/web-ide-pod-dev keeper -data $(DEV_DATA) &) && sleep 0.3 && \
	  ../bin/web-ide-pod-dev -addr $(DEV_ADDR) -data $(DEV_DATA) -allow-remote & cd web && POD=http://127.0.0.1:$(lastword $(subst :, ,$(DEV_ADDR))) npm run dev -- --host 0.0.0.0

$(DEV_DATA):
	mkdir -p $@
	[ ! -d $(HOME)/.web-ide ] || for f in $(HOME)/.web-ide/*; do \
	  case $$f in */models) ln -s $$f $@/models ;; */config.json|*/keeper.sock) ;; *) cp -r $$f $@/ ;; esac; done

test: pod/webdist/dist
	@unformatted="$$($(dir $(GO))gofmt -l pod)"; test -z "$$unformatted" || (echo "gofmt needed:"; echo "$$unformatted"; exit 1)
	cd pod && $(GO) vet ./... && $(GO) test ./...
	cd web && npm run check
	node scripts/identity-vectors.mjs
	node --test scripts/routes-test.mjs

# Browser tests (headless Chromium from the Playwright cache, or CHROME=/path/to/chrome).
e2e: build
	./e2e/run.sh

# Screenshots and GIFs of docs/images, from the recorded conversations (e2e/shots/shots.cjs).
shots: build
	cd e2e && { [ -d node_modules/playwright-core ] || npm install --no-audit --no-fund >/dev/null; }
	node e2e/shots/shots.cjs

# Installs the binary in ~/.local/bin.
install: build
	install -Dm755 $(BIN) $(HOME)/.local/bin/web-ide-pod

# Runs this build as a systemd user service started at boot (scripts/install.sh; releases are
# installed the same way: scripts/install.sh v1.0.0). The URL with the token is in
# `journalctl --user -u web-ide-pod`, or in ~/.web-ide/token.
service: build
	./scripts/install.sh --binary $(BIN)

# The hosted relay (antennae.space): this tree built for the host (Linux x86_64), installed, its
# service restarted, and the Service Worker it serves checked against this build
# (scripts/deploy-relay.sh). RELAY_VERSION=v1.11.0 deploys a published release instead of this
# tree; RELAY_ROLLBACK=1 goes back to the previous binary on the host; RELAY_HOST / RELAY_UNIT /
# RELAY_URL for another relay. The data of the relay on the host is never touched.
RELAY_HOST ?= john@5.196.7.104
RELAY_UNIT ?= antennae-relay
RELAY_URL ?= https://antennae.space
RELAY_VERSION ?=
RELAY_ROLLBACK ?=
RELAY_SETUP ?=

deploy-relay:
	@if [ -n "$(RELAY_ROLLBACK)" ]; then \
		./scripts/deploy-relay.sh --rollback --host $(RELAY_HOST) --unit $(RELAY_UNIT); \
	elif [ -n "$(RELAY_SETUP)" ]; then \
		$(MAKE) -s relay-build && \
		./scripts/deploy-relay.sh --setup --binary bin/web-ide-pod-relay --host $(RELAY_HOST) --unit $(RELAY_UNIT) --url $(RELAY_URL); \
	elif [ -n "$(RELAY_VERSION)" ]; then \
		./scripts/deploy-relay.sh $(RELAY_VERSION) --host $(RELAY_HOST) --unit $(RELAY_UNIT) --url $(RELAY_URL); \
	else \
		$(MAKE) -s relay-build && \
		./scripts/deploy-relay.sh --binary bin/web-ide-pod-relay --host $(RELAY_HOST) --unit $(RELAY_UNIT) --url $(RELAY_URL); \
	fi

relay-build: web pod/webdist/dist
	cd pod && GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../bin/web-ide-pod-relay .

# The load test of the relay (#72): a second relay on the same host, with its own service, data
# folder and a narrow TURN port range (500 relay ports), driven by pod/internal/relayload over the
# network. LOAD_TIER is static, signal or turn; LOAD_PODS, LOAD_SESSIONS, LOAD_MB, LOAD_RAMP and
# LOAD_HOLD size the run. `make load-relay-down` takes the test relay back off.
LOAD_TIER ?= signal
LOAD_PODS ?= 20
LOAD_SESSIONS ?= 100
LOAD_MB ?= 4
LOAD_RAMP ?= 30s
LOAD_HOLD ?= 60s
LOAD_TLS_PORT ?= 4443
# The host of the relay, without the user the SSH runs as.
RELAY_IP = $(lastword $(subst @, ,$(RELAY_HOST)))

relayload: pod/webdist/dist
	cd pod && $(GO) build -trimpath -o ../bin/relayload ./cmd/relayload

load-relay: relayload relay-build
	./scripts/relay-load.sh up --binary bin/web-ide-pod-relay
	./bin/relayload -relay https://$(RELAY_IP):$(LOAD_TLS_PORT) -insecure \
	  -tier $(LOAD_TIER) -pods $(LOAD_PODS) -sessions $(LOAD_SESSIONS) -mb $(LOAD_MB) \
	  -ramp $(LOAD_RAMP) -hold $(LOAD_HOLD) $(if $(filter turn,$(LOAD_TIER)),-force-turn,)

load-relay-down:
	./scripts/relay-load.sh down

clean:
	rm -rf bin pod/webdist/dist/assets
