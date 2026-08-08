BINARY  := hypercraft
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST    := internal/webui/dist

.PHONY: all build web deps run dev test lint clean cross

## build a single binary with the UI embedded
all: build

deps:
	npm --prefix web install

## build the frontend into the Go embed directory
web:
	npm --prefix web run build
	@touch $(DIST)/.gitkeep

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/hypercraft
	@echo "built ./$(BINARY) ($(VERSION))"

## build the backend only, reusing whatever UI is already in $(DIST)
build-go:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/hypercraft

run: build
	./$(BINARY) -data ./data

## backend on :8080 + Vite dev server on :5173 with hot reload
dev:
	@echo "run in two terminals:"
	@echo "  go run ./cmd/hypercraft -data ./data"
	@echo "  npm --prefix web run dev"

test:
	go test -race ./...

lint:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; \
	fi
	go vet ./...

## binaries for the usual server targets
cross: web
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/hypercraft
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 ./cmd/hypercraft
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-windows-amd64.exe ./cmd/hypercraft
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 ./cmd/hypercraft

clean:
	rm -rf $(BINARY) dist $(DIST)/assets $(DIST)/index.html
