.PHONY: build cli app dmg release test e2e e2e-ui test-all lint support

VERSION := $(shell sed -n 's/^const Version = "\(.*\)"/\1/p' internal/version/version.go)
ARCH := $(shell go env GOARCH)
APP := $(CURDIR)/desktop/build/bin/LocalSites.app
XCODE_DEV ?= /Applications/Xcode.app/Contents/Developer

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
		codesign --force --deep --sign - $(APP) && echo "Added the macOS 26 glass icon"; \
	else echo "Xcode not found: built with the classic icon only"; fi
	@# a broken Info.plist would make every in-app update fail its bundle check
	plutil -lint -s desktop/build/bin/LocalSites.app/Contents/Info.plist

# Update files for a GitHub release: the zipped app plus update.json and its signature (key in your keychain,
# see tools/release). Upload everything in dist/release to the release named v$(VERSION).
release: app
	@rm -rf dist/release && mkdir -p dist/release
	ditto -c -k --keepParent desktop/build/bin/LocalSites.app dist/release/LocalSites-$(VERSION)-macos-$(ARCH).zip
	go run ./tools/release manifest -out dist/release $(if $(SECURITY),-security) $(if $(BETA),-channel beta) dist/release/LocalSites-$(VERSION)-macos-$(ARCH).zip
	@echo "Next: gh release create v$(VERSION) dist/release/*"

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
	@! grep -n '@container\|cqw' desktop/frontend/dist/styles.css || (echo 'No CSS container queries: they make window resizing lag in WebKit. Use the size classes (docs/DESIGN.md).'; exit 1)
