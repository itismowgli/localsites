#!/bin/sh
# Creates the package signing key and prints: the armored private key, the armored public key, then
# "BINARY" and the binary public key in base64 (apt's keyring format). Piped into the release tool, which
# keeps the private key in the macOS keychain only.
set -e
export GNUPGHOME="$(mktemp -d)"
gpg --batch --quiet --pinentry-mode loopback --passphrase "" \
  --quick-gen-key "LocalSites packages <10954475+itismowgli@users.noreply.github.com>" ed25519 sign never 2>/dev/null
gpg --batch --armor --export-secret-keys
gpg --batch --armor --export
echo BINARY
gpg --batch --export | base64 -w0
echo
rm -rf "$GNUPGHOME"
