# Copyright 2026 Charles Emary
# SPDX-License-Identifier: FSL-1.1-Apache-2.0

# Build targets for hate.
# `make` (or `make all`) builds the native macOS arm64 binary AND the
# Windows x64 (.exe) cross-compile into ./dist.
# The app is pure Go (CGO not required), so cross-compilation needs no toolchain.
#
# !!! BEFORE building a new executable: bump AppVersion in
#     internal/config/config.go (1.0.1 -> 1.0.2 -> ...). It shows in Settings
#     and is how you tell a running/copied binary is the current build. !!!

BINARY  := hate
DIST    := dist
PKG     := .
LDFLAGS := -s -w
GOFLAGS := -trimpath -ldflags "$(LDFLAGS)"
VERSION := $(shell grep -oE 'AppVersion = "[^"]+"' internal/config/config.go | grep -oE '[0-9]+\.[0-9]+\.[0-9]+')

.PHONY: all native windows clean

all: native windows

# Current platform: Apple Silicon (macOS arm64)
native: $(DIST)
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -o $(DIST)/$(BINARY)-darwin-arm64 $(PKG)
	@echo "built $(DIST)/$(BINARY)-darwin-arm64  (v$(VERSION))"

# Windows, Intel/AMD 64-bit
windows: $(DIST)
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o $(DIST)/$(BINARY)-windows-amd64.exe $(PKG)
	@echo "built $(DIST)/$(BINARY)-windows-amd64.exe  (v$(VERSION))"

$(DIST):
	mkdir -p $(DIST)

clean:
	rm -rf $(DIST)
