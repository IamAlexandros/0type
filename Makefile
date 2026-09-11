# The system `go` on PATH may resolve to a gccgo wrapper instead of the real
# toolchain (seen on this dev machine: /usr/bin/go -> gccgo 1.18). Pin
# explicitly to the real toolchain so builds are never silently wrong.
GOROOT   := /usr/lib/golang
GO       := $(GOROOT)/bin/go
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)

export GOROOT

.PHONY: build test toolcheck pkgcheck clean

build: toolcheck
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/0type ./cmd/0type

test: toolcheck
	$(GO) test ./...

# Runs the golden-file ASR integration test too. Needs the real model
# present locally (`./bin/0type setup`) and takes a few seconds.
test-integration: toolcheck
	$(GO) test -tags integration ./...

# Fails loudly if the wrong Go toolchain would be used, instead of silently
# producing a broken or unexpected build.
toolcheck:
	@$(GO) version | grep -q 'go1\.2[5-9]' || \
		(echo "error: expected Go 1.25+ at $(GO), got: $$($(GO) version)" >&2; exit 1)

# Confirms the native cgo dependencies for later phases are resolvable via
# pkg-config before any code that depends on them is written.
pkgcheck:
	pkg-config --exists gtk4 && echo "gtk4: OK ($$(pkg-config --modversion gtk4))"
	pkg-config --exists x11 && echo "x11: OK ($$(pkg-config --modversion x11))"
	pkg-config --exists alsa && echo "alsa: OK ($$(pkg-config --modversion alsa))"

clean:
	rm -rf bin
