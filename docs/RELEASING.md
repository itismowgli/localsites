# Releasing

How a new version reaches people through **Settings → Updates**.

## Once: the signing key

```sh
go run ./tools/release keygen
```

This makes an ed25519 key pair. The private half goes into your login keychain (service `LocalSites`,
account `release-signing-key`). The public half is printed; it belongs in `internal/update/key.go`.
Back up the private key in your password manager. Without it, nobody can update from inside the app,
and a new key means everyone has to download the next version by hand.

## Once: the Linux package key

The `.rpm` packages and the apt and dnf repositories are signed with an OpenPGP key (ed25519). It was made
in a throwaway container and stored straight into the keychain (service `LocalSites`, account
`package-signing-key`); its public half is in `site/linux` (served by the website) and `packaging/linux` (inside
the packages). Back the private key up in your password manager too. To make a new one (rarely; installed
systems then need the new public key):

```sh
docker build -t localsites-repo-tools tests/linux/repo
docker run --rm localsites-repo-tools new-key.sh | go run ./tools/release package-key
```

## Once: the macOS code-signing certificate

macOS remembers folder permissions (Documents, Desktop, Downloads) by who signed an app. An app signed ad hoc
has a new identity with every build, so each update asked for those permissions again. Every release is now
signed with one self-signed certificate, **LocalSites Code Signing** (valid until 2036), kept in the login
keychain. The app's identity becomes "com.wails.localsites signed by that certificate", the same for every
version, and permissions stay granted.

- `make app` signs with it when it's in the keychain and signs ad hoc otherwise; `make release` refuses to run
  without it.
- **Back it up**: Keychain Access → My Certificates → LocalSites Code Signing → Export as `.p12`, and keep the
  file and its password in your password manager. A new certificate would be a new identity again: every
  user would be asked for folder permissions once more.
- It doesn't replace an Apple Developer ID: a downloaded copy still needs Open Anyway the first time.

## The release pattern

Every release looks the same, so nothing stands out as different:

- **Version**: semantic versioning. A new feature raises the middle number (1.5.0 → 1.6.0), a fix only the last
  (1.2.3 → 1.2.4). Breaking changes would raise the first.
- **Tag**: `vX.Y.Z`, annotated, its message the release title. Only made from a clean tree that is on `master`
  (`go run ./tools/release tag` refuses anything else).
- **Title**: `LocalSites X.Y.Z: <title>`, the title from `internal/version/changelog.json` (no colon inside it).
- **Notes**: the changelog items grouped under New, Improved, Fixed and Security (each item's `kinds` entry
  says which), then Install, Verify (`SHA256SUMS`) and a "Full changes" link to the release before.
- **Files**: `LocalSites-X.Y.Z-macos-arm64.zip`, the `.deb` and `.rpm` packages (their names follow each
  distro's convention), `update.json` and its signature, and `SHA256SUMS` for all of them.
- **Changelog**: `CHANGELOG.md` is written from `changelog.json` (`go run ./tools/release changelog`); `make lint`
  fails when it's out of date, and a test fails when an item has no kind.
- **Copy**: every item is one plain sentence, passed through the humanizer before it's written.
- The `linux-repo` release isn't a version: it holds the apt repository that installed packages point at, so its
  tag must never change.

`go run ./tools/release notes-sync` rewrites every published release's title and notes in this shape and adds a
missing `SHA256SUMS`; use it after changing the template.

## Every release

1. Bump `internal/version/version.go` and add an entry to `internal/version/changelog.json`.
   Its title and items become the release notes people see before installing.
2. Run the full test gate (`make test-all`).
3. Merge into `master` and push. Start Docker Desktop (the Linux repositories are built in a container).
4. `make release` (add `SECURITY=1` for a security fix, `BETA=1` for the beta channel). It:
   - pushes the tag `vX.Y.Z`, which starts GitHub building the Linux programs (`.github/workflows/linux-packages.yml`);
   - builds and zips the Mac app (`ditto`, so the code signature survives);
   - waits for GitHub, downloads the Linux programs and packages them here as `.deb` and `.rpm` (nfpm), the `.rpm`
     signed with the package key from the keychain;
   - builds the signed apt repository (`dist/release/apt`) and dnf metadata (`site/linux/rpm`);
   - writes `dist/release/update.json` with its signature, covering the Mac zip and every Linux package.
5. Check `dist/release`, then `make publish`: it creates the GitHub release with notes from the changelog,
   uploads the apt repository to the `linux-repo` release (a prerelease that only apt reads) and commits the dnf
   metadata, which goes live with the website.

The keys never leave this Mac: they reach nfpm and the container as a temporary file or on stdin.

## How Linux installs update

The `.deb` adds `/etc/apt/sources.list.d/localsites.list` and its key; the `.rpm` adds
`/etc/yum.repos.d/localsites.repo` and its key. Later versions then arrive with the system's own updates, with
signatures checked by apt and dnf. The app's own updater (Settings → Updates) also works: it downloads the
package, checks it against the signed `update.json`, and installs it after one password prompt.

The repository must be public, or the app can't read the release list.

## What the app checks before installing

- the manifest's signature against the key built into the running app (releases without a valid signature are ignored)
- the download's size and SHA-256 against the signed manifest
- that the new app has LocalSites' bundle id and the expected version, and that it runs on this Mac (`update-selftest`)
- the same checks again right before the swap

Then the app and the service quit, a helper moves the current app to `updates/previous` (for switching back),
puts the new one in its place, and starts LocalSites again. If a step fails, the old app is put back.
Settings are copied to `updates/config-backups` first. A settings file from a newer version is refused, not misread.

Signing and notarizing the app itself (so macOS doesn't warn on the first download) needs an Apple Developer ID.
In-app updates don't depend on it: they are downloaded by LocalSites, not by a browser, so they carry no quarantine flag.
