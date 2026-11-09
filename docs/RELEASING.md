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

## Every release

1. Bump `internal/version/version.go` and add an entry to `internal/version/changelog.json`.
   Its title and items become the release notes people see before installing.
2. Run the full test gate (`make test-all`).
3. `make release` (add `SECURITY=1` for a security fix, `BETA=1` for the beta channel). This builds the app,
   zips it (`ditto`, so the code signature survives) and writes `dist/release/update.json` with its signature.
4. `gh release create vX.Y.Z dist/release/*` (`--prerelease` for beta).

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
