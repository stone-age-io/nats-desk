BINARY      := nats-desk
CMD         := ./cmd/nats-desk
VERSION     ?= dev
DIST        := dist
FRONTEND    := frontend

# CGO stays off everywhere. It is what lets every target below cross-compile
# from one machine, and it is the main reason this is not a webview app.
export CGO_ENABLED = 0

LDFLAGS := -s -w -X github.com/stone-age-io/nats-desk/internal/buildinfo.Version=$(VERSION)

# -H=windowsgui links against the GUI subsystem, which is the whole reason
# double-clicking the exe no longer flashes up a console window. The cost is
# that the process gets no standard handles at all, so nothing can be printed
# and the log has to go to a file - see internal/applog, which detects exactly
# this and switches destination on its own.
#
# It is applied per target rather than globally: `make dev` runs `go run`
# without GOFLAGS, and a console build is what you want while iterating.
WINLDFLAGS := -H=windowsgui

# LDFLAGS itself stays platform-neutral, because build-all reuses it for every
# target and -H=windowsgui is a link error anywhere else.
NATIVE_LDFLAGS := $(LDFLAGS)
ifeq ($(shell go env GOOS),windows)
NATIVE_LDFLAGS += $(WINLDFLAGS)
endif

GOFLAGS := -trimpath -ldflags "$(NATIVE_LDFLAGS)"

.PHONY: all build ui build-all winres test test-race test-coverage fmt lint deps clean install-tools dev run

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
		os=$${t%/*}; arch=$${t#*/}; ext=""; lf="$(LDFLAGS)"; \
		if [ "$$os" = "windows" ]; then ext=".exe"; lf="$$lf $(WINLDFLAGS)"; fi; \
		echo "  $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$$lf" \
			-o $(DIST)/$(BINARY)-$$os-$$arch$$ext $(CMD); \
	done
	@echo "built:"; ls -lh $(DIST)

## winres: regenerate the Windows icon and version resources
##
## Deliberately not a dependency of build. The generated .syso files are
## committed, so a plain `go build` still produces an exe with an icon, and
## making a downloaded tool part of every release build would contradict the
## one-machine no-extra-toolchain rule the rest of this file follows. Run it
## when the icon changes, or when cutting a release if the version shown in
## the file properties matters. VERSION only reaches the properties dialog;
## the version the app itself reports comes from LDFLAGS above.
GOVERSIONINFO := github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
winres:
	cd $(CMD) && go run $(GOVERSIONINFO) \
		-file-version "$(VERSION)" -product-version "$(VERSION)" \
		-o resource_windows_amd64.syso versioninfo.json
	cd $(CMD) && go run $(GOVERSIONINFO) -arm -64 \
		-file-version "$(VERSION)" -product-version "$(VERSION)" \
		-o resource_windows_arm64.syso versioninfo.json
	@echo "regenerated; commit the .syso files"

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
