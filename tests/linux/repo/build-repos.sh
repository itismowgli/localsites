#!/bin/sh
# build-repos.sh <rpm base URL> < private key
# /in: the .deb and .rpm files. Writes /out/apt (a flat, signed apt repository for the "linux-repo" GitHub
# release) and /out/rpm/repodata (signed dnf metadata whose packages are downloaded from <rpm base URL>).
set -e
export GNUPGHOME="$(mktemp -d)"
trap 'rm -rf "$GNUPGHOME"' EXIT
gpg --batch --quiet --import
KEY=$(gpg --batch --list-secret-keys --with-colons | awk -F: '/^fpr/ {print $10; exit}')
sign() { gpg --batch --yes --pinentry-mode loopback --passphrase "" --local-user "$KEY" "$@"; }

rm -rf /out/apt /out/rpm && mkdir -p /out/apt /out/rpm
cp /in/*.deb /out/apt/
cd /out/apt
dpkg-scanpackages --multiversion . /dev/null > Packages 2>/dev/null
gzip -9kn Packages
apt-ftparchive -o APT::FTPArchive::Release::Origin=LocalSites -o APT::FTPArchive::Release::Label=LocalSites \
  -o APT::FTPArchive::Release::Suite=stable -o APT::FTPArchive::Release::Codename=stable release . > Release
sign --clearsign -o InRelease Release
sign --armor --detach-sign -o Release.gpg Release

mkdir -p /tmp/rpms && cp /in/*.rpm /tmp/rpms/
createrepo_c --quiet --baseurl "$1" -o /out/rpm /tmp/rpms
sign --armor --detach-sign -o /out/rpm/repodata/repomd.xml.asc /out/rpm/repodata/repomd.xml
echo "apt: $(ls /out/apt | tr '\n' ' ')"
echo "rpm: $(ls /out/rpm/repodata | tr '\n' ' ')"
