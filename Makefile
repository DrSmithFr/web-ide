# Web IDE: pod Go (single binary) + front SolidJS embedded in it.
GO ?= $(shell command -v go || echo $(HOME)/sdk/go/bin/go)
BIN := bin/web-ide-pod
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build web pod run dev test e2e shots check clean install service

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
dev: pod/webdist/dist $(DEV_DATA)
	cd pod && $(GO) run . -addr $(DEV_ADDR) -data $(DEV_DATA) -allow-remote & cd web && POD=http://127.0.0.1:$(lastword $(subst :, ,$(DEV_ADDR))) npm run dev -- --host 0.0.0.0

$(DEV_DATA):
	mkdir -p $@
	[ ! -d $(HOME)/.web-ide ] || for f in $(HOME)/.web-ide/*; do \
	  case $$f in */models) ln -s $$f $@/models ;; */config.json) ;; *) cp -r $$f $@/ ;; esac; done

test: pod/webdist/dist
	cd pod && $(GO) vet ./... && $(GO) test ./...
	cd web && npm run check

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

clean:
	rm -rf bin pod/webdist/dist/assets
