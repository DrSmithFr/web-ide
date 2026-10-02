# Web IDE: pod Go (single binary) + front SolidJS embedded in it.
GO ?= $(shell command -v go || echo $(HOME)/sdk/go/bin/go)
BIN := bin/web-ide-pod

.PHONY: build web pod run dev test check clean

build: web pod

web:
	cd web && npm install --no-audit --no-fund && npm run build

# go:embed needs the folder, even before the first front build.
pod/webdist/dist:
	mkdir -p $@ && [ -e $@/index.html ] || echo '<!doctype html><p>Front non compilé : make web</p>' > $@/index.html

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

clean:
	rm -rf bin pod/webdist/dist/assets
