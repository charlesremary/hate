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

.PHONY: all native windows clean sign release cask publish

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
	rm -rf $(DIST) build

# ---------------------------------------------------------------------------
# macOS release: sign, notarize, zip, update the Homebrew cask.
# One-time setup (Developer ID Application certificate, notarytool keychain
# profile): Obsidian vault, hate/"Signing and notarizing the macOS executable".
#
#   make release      sign + zip + notarize dist/hate-darwin-arm64, then `make cask`
#   make cask         write AppVersion and the zip's sha256 into Casks/hate.rb
#   make publish      commit the cask + signed binary, tag v<AppVersion>, create
#                     the GitHub release with the zip, push main
#
# A release: bump AppVersion (and its pin in internal/api/plan_test.go), commit,
# then `make release` and `make publish`.
# ---------------------------------------------------------------------------
GIT_NAME       ?= Chuck Emary
GIT_EMAIL      ?= you@example.com
IDENTITY       ?= Developer ID Application: CHARLES ROBERT EMARY (8G48C7PL22)
NOTARY_PROFILE ?= hate-notary
BUNDLE_ID      := com.charlesemary.hate
ZIP            := $(DIST)/$(BINARY)-darwin-arm64.zip
CASK           := Casks/hate.rb

# Sign with the hardened runtime and a secure timestamp (both required for
# notarization), then verify.
sign: native
	codesign --force --options runtime --timestamp --identifier $(BUNDLE_ID) \
	  --sign "$(IDENTITY)" $(DIST)/$(BINARY)-darwin-arm64
	codesign --verify --strict --verbose=2 $(DIST)/$(BINARY)-darwin-arm64

# Zip the signed binary as plain "hate" (what the cask installs) with ditto,
# notarize it (waits for Apple), and update the cask. A bare binary can't be
# stapled; Gatekeeper checks the notarization online on first run.
release: sign
	rm -rf build/release && mkdir -p build/release
	cp $(DIST)/$(BINARY)-darwin-arm64 build/release/$(BINARY)
	rm -f $(ZIP)
	ditto -c -k build/release/$(BINARY) $(ZIP)
	xcrun notarytool submit $(ZIP) --keychain-profile "$(NOTARY_PROFILE)" --wait
	@$(MAKE) --no-print-directory cask
	@echo "released $(ZIP) (v$(VERSION)); next: make publish"

cask:
	@test -f $(ZIP) || { echo "no $(ZIP): run make release first"; exit 1; }
	sed -i '' -E 's/^  version "[^"]*"/  version "$(VERSION)"/' $(CASK)
	sed -i '' -E "s/^  sha256 \"[0-9a-f]*\"/  sha256 \"$$(shasum -a 256 $(ZIP) | cut -d' ' -f1)\"/" $(CASK)
	@grep -E '^  (version|sha256) ' $(CASK)

# Publish the release built by `make release`. Refuses to run unless: on main,
# gh is logged in, the tag is new, the only changes are the cask and the dist
# binaries, the cask matches AppVersion and the zip, and Apple confirms the
# binary is notarized (codesign -R notarized; spctl can't be used, it rejects
# every bare binary as "not an app"). Order: commit, tag, push the tag, create
# the GitHub release with the zip, and only then push main, so the cask on main
# never points at a zip that isn't uploaded yet.
publish:
	@test -f $(ZIP) || { echo "no $(ZIP): run make release first"; exit 1; }
	@test "$$(git rev-parse --abbrev-ref HEAD)" = main || { echo "not on main"; exit 1; }
	@gh auth status >/dev/null 2>&1 || { echo "gh is not logged in: run gh auth login"; exit 1; }
	@! git rev-parse -q --verify refs/tags/v$(VERSION) >/dev/null || { echo "tag v$(VERSION) already exists: bump AppVersion"; exit 1; }
	@test -z "$$(git ls-remote --tags origin v$(VERSION))" || { echo "tag v$(VERSION) already exists on origin"; exit 1; }
	@test -z "$$(git status --porcelain -- . ':!$(CASK)' ':!$(DIST)')" || { echo "uncommitted changes besides the cask and dist/: commit them first"; git status --short; exit 1; }
	@grep -q '^  version "$(VERSION)"' $(CASK) || { echo "$(CASK) is not at v$(VERSION): run make cask"; exit 1; }
	@grep -q "^  sha256 \"$$(shasum -a 256 $(ZIP) | cut -d' ' -f1)\"" $(CASK) || { echo "$(CASK) sha256 doesn't match $(ZIP): run make cask"; exit 1; }
	@codesign --verify --check-notarization -R="notarized" build/release/$(BINARY) 2>/dev/null || { echo "build/release/$(BINARY) is not notarized: run make release"; exit 1; }
	git add $(CASK)
	git add -u $(DIST)
	git -c user.name="$(GIT_NAME)" -c user.email="$(GIT_EMAIL)" commit -m "Release v$(VERSION)"
	git tag -a v$(VERSION) -m "hate v$(VERSION)"
	git push origin v$(VERSION)
	gh release create v$(VERSION) $(ZIP) --verify-tag --title "hate v$(VERSION)" --generate-notes
	git push origin main
	@echo "published v$(VERSION): brew update && brew upgrade --cask hate"
