.PHONY: build cli app dmg release publish linux-binaries linux-packages test e2e e2e-ui test-all lint support

VERSION := $(shell sed -n 's/^const Version = "\(.*\)"/\1/p' internal/version/version.go)
ARCH := $(shell go env GOARCH)
APP := $(CURDIR)/desktop/build/bin/LocalSites.app
XCODE_DEV ?= /Applications/Xcode.app/Contents/Developer

# Every release is signed with the "LocalSites Code Signing" certificate from the keychain. macOS remembers
# folder permissions (Documents, Desktop…) by the signer, so they stay granted across updates; an app signed
# ad hoc is a new app to macOS with every build. Builds on machines without the certificate are signed ad hoc.
MAC_SIGN := $(shell security find-identity 2>/dev/null | awk '/"LocalSites Code Signing"/ { print $$2; exit }')

# Where problem reports go: the Sentry project pixel-perfection-ru/localsites. Reports are only sent
# when the person agrees (Settings → Problems). Plain `go build`/`go run` builds leave it empty.
# Set REPORT_DSN= to build without it.
REPORT_DSN ?= https://7a3f7a468d88b9e425111ce8155427b4@o4512359534624768.ingest.de.sentry.io/4512362344874064
LDFLAGS := -s -w -X github.com/localsites/localsites/internal/problems.DSN=$(REPORT_DSN)

build: cli app

cli:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/localsites ./cmd/localsites

app:
	@# the bundle's version must match the app's: updates check it before installing
	@sed -i '' 's/"productVersion": "[^"]*"/"productVersion": "$(VERSION)"/' desktop/wails.json
	cd desktop && wails build -clean -trimpath -ldflags "$(LDFLAGS)"
	@# macOS 26 Liquid Glass icon from desktop/build/LocalSites.icon (Icon Composer). Older macOS uses iconfile.icns.
	@# Needs Xcode for actool; without it the app is built with the classic icon only.
	@if DEVELOPER_DIR=$(XCODE_DEV) xcrun --find actool >/dev/null 2>&1; then \
		DEVELOPER_DIR=$(XCODE_DEV) xcrun actool $(CURDIR)/desktop/build/LocalSites.icon --compile $(APP)/Contents/Resources \
			--platform macosx --minimum-deployment-target 11.0 --app-icon LocalSites \
			--output-partial-info-plist $(CURDIR)/desktop/build/.icon-info.plist >/dev/null && \
		rm -f $(APP)/Contents/Resources/LocalSites.icns $(CURDIR)/desktop/build/.icon-info.plist && \
		echo "Added the macOS 26 glass icon"; \
	else echo "Xcode not found: built with the classic icon only"; fi
	@# with the certificate, also the hardened runtime: no other program can load code into LocalSites, so
	@# the service can trust a caller signed like itself (see internal/api/peer_darwin.go)
	codesign --force --deep $(if $(MAC_SIGN),--options runtime) --sign "$(or $(MAC_SIGN),-)" $(APP)
	@if [ -z "$(MAC_SIGN)" ]; then echo "Signed ad hoc (no LocalSites Code Signing certificate in the keychain)"; fi
	@# a broken Info.plist would make every in-app update fail its bundle check
	plutil -lint -s desktop/build/bin/LocalSites.app/Contents/Info.plist

# Update files for a GitHub release: the zipped app plus update.json and its signature (key in your keychain,
# see tools/release). Upload everything in dist/release to the release named v$(VERSION).
release:
	@test -n "$(MAC_SIGN)" || { echo "The LocalSites Code Signing certificate isn't in the keychain (see docs/RELEASING.md)"; exit 1; }
	go run ./tools/release tag            # pushes v$(VERSION); GitHub starts building the Linux programs
	$(MAKE) app
	@rm -rf dist/release && mkdir -p dist/release
	ditto -c -k --keepParent desktop/build/bin/LocalSites.app dist/release/LocalSites-$(VERSION)-macos-$(ARCH).zip
	go run ./tools/release linux -out dist/release   # waits for GitHub, packages .deb/.rpm here, signs the .rpm
	go run ./tools/release repos -out dist/release   # the signed apt and dnf repositories (needs Docker)
	go run ./tools/release manifest -out dist/release $(if $(SECURITY),-security) $(if $(BETA),-channel beta) dist/release/LocalSites-$(VERSION)-macos-$(ARCH).zip dist/release/*.deb dist/release/*.rpm
	go run ./tools/release sums -out dist/release      # SHA256SUMS for every file
	@echo "Check dist/release, then: make publish"

# Publishes what make release made: the GitHub release, the apt repository and the dnf metadata.
publish:
	go run ./tools/release publish -out dist/release

# Linux programs for this machine's architecture: the app, the CLI and the icons (GitHub builds these for
# each release; see .github/workflows/linux-packages.yml). Needs GTK, WebKitGTK 4.1, wails and ImageMagick.
linux-binaries:
	@test "$$(go env GOOS)" = linux || { echo "build the Linux programs on Linux (see tests/linux/build)"; exit 1; }
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/localsites-linux ./cmd/localsites
	cd desktop && wails build -tags webkit2_41 -trimpath -ldflags "$(LDFLAGS)" -o LocalSites
	@mkdir -p dist/linux
	convert desktop/build/appicon.png -resize 256x256 dist/linux/localsites-256.png
	convert desktop/build/appicon.png -resize 512x512 dist/linux/localsites-512.png

# Linux packages from those programs, on this machine (tests; releases package them on the Mac, see release).
linux-packages: linux-binaries
	@mkdir -p dist/release
	VERSION=$(VERSION) ARCH=$$(go env GOARCH) RPM_SIGNING_KEY_FILE= nfpm package -f packaging/linux/nfpm.yaml -p deb -t dist/release/
	VERSION=$(VERSION) ARCH=$$(go env GOARCH) RPM_SIGNING_KEY_FILE= nfpm package -f packaging/linux/nfpm.yaml -p rpm -t dist/release/

# Disk image for sharing. Unsigned unless SIGN_ID is set (a "Developer ID Application" identity);
# notarizing needs an Apple Developer account: xcrun notarytool submit dist/LocalSites.dmg --wait …
dmg: app
	@mkdir -p dist && rm -rf dist/dmg && mkdir dist/dmg
	cp -R desktop/build/bin/LocalSites.app dist/dmg/
	@if [ -n "$(SIGN_ID)" ]; then codesign --deep --force --options runtime --timestamp --sign "$(SIGN_ID)" dist/dmg/LocalSites.app; fi
	ln -s /Applications dist/dmg/Applications
	hdiutil create -volname LocalSites -srcfolder dist/dmg -ov -format UDZO dist/LocalSites.dmg
	@rm -rf dist/dmg && echo "dist/LocalSites.dmg"

# Support options (GitHub Sponsors, Razorpay, Buy Me a Coffee): turn them on or off in .github/FUNDING.yml,
# then run this to update the README. The website follows the same file when it's published.
support:
	node tools/support.mjs

# Unit tests: fast, no services
test:
	go test ./...

# Real Apache, PHP and MySQL in a throwaway sandbox (never touches your Valet, Keychain or MAMP)
e2e:
	go test -tags e2e -count=1 -timeout 15m ./e2e/

# The real desktop UI in WebKit (macOS app engine) and Chromium (Windows/Linux) against a sandboxed service
e2e-ui:
	cd tests/ui && npm install --silent && npx playwright install webkit chromium && npx playwright test

test-all: lint test e2e e2e-ui

lint:
	@test -z "$$(gofmt -l cmd internal desktop e2e)" || (gofmt -l cmd internal desktop e2e; exit 1)
	go vet ./...
	node --check desktop/frontend/dist/app.js
	node tools/support.mjs --check
	@v=$$(sed -n 's/^go \([0-9]*\.[0-9]*\).*/\1/p' go.mod); grep -q "badge/go-$$v-" README.md || (echo "README Go badge doesn't say $$v (go.mod)"; exit 1)
	node tools/build-site.mjs --check   # site/*.html built from site-src/
	node tools/build-tokens.mjs --check   # tokens.css current; no raw colors in the app or the site
	go run ./tools/release changelog -check   # CHANGELOG.md matches changelog.json
	@! grep -n '@container\|cqw' desktop/frontend/dist/styles.css || (echo 'No CSS container queries: they make window resizing lag in WebKit. Use the size classes (docs/DESIGN.md).'; exit 1)
