# Web IDE: pod Go (single binary) + front SolidJS embedded in it.
GO ?= $(shell command -v go || echo $(HOME)/sdk/go/bin/go)
BIN := bin/web-ide-pod

.PHONY: build web pod run dev test e2e check clean install service

build: web pod

web:
	cd web && npm install --no-audit --no-fund && npm run build

# go:embed needs the folder, even before the first front build.
pod/webdist/dist:
	mkdir -p $@ && [ -e $@/index.html ] || echo '<!doctype html><p>Front end not built: run make web</p>' > $@/index.html

pod: pod/webdist/dist
	cd pod && $(GO) build -trimpath -ldflags "-s -w" -o ../$(BIN) .

run: build
	./$(BIN)

# Development: the pod serves the API, Vite serves the front with hot reload.
# Both accept other machines (-allow-remote): open http://<host>:5173/?token=… from anywhere,
# the pairing goes through the Vite proxy. The token stays the only protection.
DEV_ADDR ?= 0.0.0.0:4433
dev: pod/webdist/dist
	cd pod && $(GO) run . -addr $(DEV_ADDR) -allow-remote & cd web && npm run dev -- --host 0.0.0.0

test: pod/webdist/dist
	cd pod && $(GO) vet ./... && $(GO) test ./...
	cd web && npm run check

# Browser tests (headless Chromium from the Playwright cache, or CHROME=/path/to/chrome).
e2e: build
	./e2e/run.sh

# Installs the binary in ~/.local/bin.
install: build
	install -Dm755 $(BIN) $(HOME)/.local/bin/web-ide-pod

# Starts the pod with the user session (systemd user service). The URL with the token is in
# `journalctl --user -u web-ide-pod`, or in ~/.web-ide/token.
service: install
	install -Dm644 pod/web-ide-pod.service $(HOME)/.config/systemd/user/web-ide-pod.service
	systemctl --user daemon-reload
	systemctl --user enable --now web-ide-pod
	systemctl --user restart web-ide-pod

clean:
	rm -rf bin pod/webdist/dist/assets
