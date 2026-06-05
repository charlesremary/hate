# Build targets for hate.
# `make` (or `make all`) builds the native macOS arm64 binary AND the
# Windows x64 (.exe) cross-compile into ./dist.
# The app is pure Go (CGO not required), so cross-compilation needs no toolchain.

BINARY  := hate
DIST    := dist
PKG     := .
LDFLAGS := -s -w
GOFLAGS := -trimpath -ldflags "$(LDFLAGS)"

.PHONY: all native windows clean

all: native windows

# Current platform: Apple Silicon (macOS arm64)
native: $(DIST)
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -o $(DIST)/$(BINARY)-darwin-arm64 $(PKG)
	@echo "built $(DIST)/$(BINARY)-darwin-arm64"

# Windows, Intel/AMD 64-bit
windows: $(DIST)
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o $(DIST)/$(BINARY)-windows-amd64.exe $(PKG)
	@echo "built $(DIST)/$(BINARY)-windows-amd64.exe"

$(DIST):
	mkdir -p $(DIST)

clean:
	rm -rf $(DIST)
