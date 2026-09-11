# The system `go` on PATH may resolve to a gccgo wrapper instead of the real
# toolchain (seen on this dev machine: /usr/bin/go -> gccgo 1.18). Pin
# explicitly to the real toolchain so builds are never silently wrong.
GOROOT   := /usr/lib/golang
GO       := $(GOROOT)/bin/go
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)

export GOROOT

# Where the release tarball is staged, and what goes in it.
DISTNAME := 0type-$(VERSION)-linux-$(shell uname -m)
DISTDIR  := dist/$(DISTNAME)
ORTLIB   := $(shell readlink -f /usr/lib64/libonnxruntime.so 2>/dev/null)

.PHONY: build test test-integration toolcheck pkgcheck dist clean

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

# Builds the distributable tarball: the binary, the ONNX Runtime shared
# library it needs, the themes as editable files, and an installer.
#
# This is deliberately not a single static executable. Two of 0type's
# three native dependencies (GTK4, ALSA) are on every Linux desktop
# already and should be used from the system rather than shipped; the
# third (ONNX Runtime) is neither universally installed nor version-stable,
# so it travels with the binary and is found relative to it at runtime
# (see internal/asr's bundledLibraryPaths). The speech model isn't in here
# either -- it's ~650MB and `0type setup` fetches it once.
#
# The themes/ copies are for reading and cribbing from: the binary has its
# own embedded copies and doesn't read this directory.
dist: build
	@test -n "$(ORTLIB)" || (echo "error: libonnxruntime.so not found; install onnxruntime-devel" >&2; exit 1)
	rm -rf $(DISTDIR)
	mkdir -p $(DISTDIR)/bin $(DISTDIR)/lib $(DISTDIR)/themes
	cp bin/0type $(DISTDIR)/bin/
	cp $(ORTLIB) $(DISTDIR)/lib/
	ln -sf $(notdir $(ORTLIB)) $(DISTDIR)/lib/libonnxruntime.so
	cp internal/theme/themes/*.css $(DISTDIR)/themes/
	cp packaging/install.sh $(DISTDIR)/
	chmod +x $(DISTDIR)/install.sh
	cp README.md $(DISTDIR)/
	tar -czf dist/$(DISTNAME).tar.gz -C dist $(DISTNAME)
	@echo
	@echo "built dist/$(DISTNAME).tar.gz ($$(du -h dist/$(DISTNAME).tar.gz | cut -f1))"

clean:
	rm -rf bin dist
