BINARY      := nats-desk
CMD         := ./cmd/nats-desk
VERSION     ?= dev
DIST        := dist
FRONTEND    := frontend

# CGO stays off everywhere. It is what lets every target below cross-compile
# from one machine, and it is the main reason this is not a webview app.
export CGO_ENABLED = 0

LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath -ldflags "$(LDFLAGS)"

.PHONY: all build ui build-all test test-race test-coverage fmt lint deps clean install-tools dev run

all: build

## ui: build the single-page app into frontend/dist for //go:embed
##
## The .gitkeep restore is not decoration: vite's emptyOutDir wipes dist on
## every build, and that placeholder is what makes //go:embed all:dist resolve
## on a fresh clone that has not run this target yet. Without it, cloning and
## running `go build` fails before you get anywhere near npm.
ui:
	cd $(FRONTEND) && npm install --silent && npm run build
	@touch $(FRONTEND)/dist/.gitkeep

## build: frontend + binary for the current platform
build: ui
	go build $(GOFLAGS) -o $(BINARY)$(shell go env GOEXE) $(CMD)

## build-all: every supported target, from whichever machine runs this
build-all: ui
	@mkdir -p $(DIST)
	@set -e; \
	for t in \
		linux/amd64 linux/arm64 \
		darwin/amd64 darwin/arm64 \
		windows/amd64 windows/arm64 \
		freebsd/amd64 ; do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "  $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) \
			-o $(DIST)/$(BINARY)-$$os-$$arch$$ext $(CMD); \
	done
	@echo "built:"; ls -lh $(DIST)

## dev: run the backend for frontend iteration; pair with `npm run dev`
dev:
	go run $(CMD) -dev -v

## run: build and start normally
run: build
	./$(BINARY)$(shell go env GOEXE)

## test: the default, and it runs anywhere
##
## Deliberately no -race: the race detector requires cgo, which contradicts the
## global CGO_ENABLED=0 above and needs a C toolchain that a Windows dev box
## typically does not have. `make test` failing to even build is worse than
## running without the detector, so race is opt-in below.
test:
	go test -cover ./...

## test-race: needs cgo and a C compiler (gcc/clang) on PATH
test-race:
	CGO_ENABLED=1 go test -race -cover ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "wrote coverage.html"

fmt:
	gofmt -w .
	go run golang.org/x/tools/cmd/goimports@latest -w .

lint:
	go vet ./...
	golangci-lint run

deps:
	go mod download
	go mod tidy

clean:
	rm -rf $(DIST) $(FRONTEND)/dist/assets coverage.out coverage.html
	rm -f $(BINARY) $(BINARY).exe

install-tools:
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
