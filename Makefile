BINARY  := hypercraft
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST    := internal/webui/dist
# Archive names carry the bare version, tags carry the leading v. The self
# updater derives the same name from the release tag, so the two must agree —
# see selfupdate.AssetName.
PKGVER  := $(VERSION:v%=%)

.PHONY: all build web deps run dev test lint clean cross package

## build a single binary with the UI embedded
all: build

deps:
	npm --prefix web install

## build the frontend into the Go embed directory
##
## The .gitkeep that //go:embed needs is restored by the build itself (see the
## keep-embed-anchor plugin in web/vite.config.ts), so `npm run build` on its own
## is safe too.
web:
	npm --prefix web run build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/hypercraft
	@echo "built ./$(BINARY) ($(VERSION))"

## build the backend only, reusing whatever UI is already in $(DIST)
build-go:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/hypercraft

run: build
	./$(BINARY) -data ./data

## backend on :19190 + Vite dev server on :5173 with hot reload
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

## release archives + checksums in release/, one per cross target
##
## Lives here rather than in the workflow so the release and snapshot jobs
## cannot drift apart, and so a release build can be reproduced locally.
package: cross
	@rm -rf release && mkdir -p release
	@set -eu; \
	for bin in dist/$(BINARY)-*; do \
		target=$${bin#dist/$(BINARY)-}; \
		target=$${target%.exe}; \
		stage="$(BINARY)-$(PKGVER)-$${target}"; \
		mkdir -p "$$stage"; \
		case "$$bin" in \
			*.exe) cp "$$bin" "$$stage/$(BINARY).exe" ;; \
			*)     cp "$$bin" "$$stage/$(BINARY)"; chmod +x "$$stage/$(BINARY)" ;; \
		esac; \
		cp README.md CHANGELOG.md LICENSE "$$stage/"; \
		case "$$target" in \
			linux-*) cp deploy/$(BINARY).service "$$stage/" ;; \
		esac; \
		case "$$target" in \
			windows-*) zip -qr "release/$${stage}.zip" "$$stage" ;; \
			*)         tar -czf "release/$${stage}.tar.gz" "$$stage" ;; \
		esac; \
		rm -rf "$$stage"; \
	done; \
	cd release && sha256sum $(BINARY)-* > SHA256SUMS.txt
	@ls -lh release

clean:
	rm -rf $(BINARY) dist release $(DIST)/assets $(DIST)/index.html
