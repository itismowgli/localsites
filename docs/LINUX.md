# LocalSites for Linux: plan

Status: started 2026-11-09 on the `phase-1` branch. `master` stays on the released macOS version until Linux is
ready to ship.

## Decisions

### Which distros first

Ubuntu 24.04 LTS and Debian 12 or newer come first, then Fedora. We publish a `.deb` and an `.rpm` built from one
package config. Other distros that use systemd will probably work, but we only test these.

This matches what comparable tools do. Local publishes a Debian/Ubuntu build and a Red Hat/Fedora build. Lerd
publishes an Ubuntu PPA and a Fedora COPR. Most PHP tutorials for Linux assume Ubuntu.

### Where PHP, MySQL and Apache come from

LocalSites downloads and manages its own PHP and MySQL, and uses the distro's Apache:

| Runtime | Source | Why |
|---|---|---|
| PHP 7.4 to 8.5 | Static builds made with [static-php-cli](https://github.com/crazywhalecc/static-php-cli), CLI and FPM | The same versions on every distro, side by side, with no PPA. Laravel Herd ships PHP the same way. |
| MySQL 8.4 LTS | Oracle's official generic Linux build (`linux-glibc2.28`, x86_64 and aarch64) | Runs on any distro with glibc 2.28 or newer, supported until 2032. |
| Apache | The distro package (`apache2` on Debian and Ubuntu, `httpd` on Fedora) | Every distro ships it and keeps it patched. We only need one version, for `.htaccess`. |

If a project needs a PHP extension that the static build doesn't include, LocalSites can use the system PHP
instead (for example from the distro or the ondrej/php repository). Downloads are checked against a SHA-256 list
signed with the same key as app updates.

Reasons for this choice over the alternatives:

- **Distro packages only** tie PHP versions to each distro's release cycle. On Ubuntu, side-by-side versions
  depend on one volunteer-maintained repository.
- **Containers** (Lerd uses rootless Podman) need a container runtime and use much more memory. LocalSites
  promises a small, quiet background service.
- **Homebrew on Linux** works, but few Linux developers have it.

Later, macOS can use the same managed runtimes, which would remove the Homebrew requirement there too.

## How the Mac-only parts map to Linux

| Area | macOS today | Linux |
|---|---|---|
| Passwords | Keychain (`security`) | Secret Service over D-Bus (GNOME Keyring, KWallet); when neither runs, the existing file store, which only your user account can read |
| `.test` DNS | `/etc/resolver/test` | A systemd-resolved drop-in, `/etc/systemd/resolved.conf.d/localsites.conf` with `DNS=127.0.0.1:<port>` and `Domains=~test`. Needs sudo once. |
| Ports 80 and 443 | Valet or the built-in proxy | `setcap cap_net_bind_service=+ep` on the service binary, set by the package installer |
| Certificate trust | System keychain | The system store (`update-ca-certificates` or `update-ca-trust`) plus the NSS databases Chrome and Firefox read (`certutil`) |
| Start at login | launchd agent | A systemd user unit |
| Notifications | `osascript` | `notify-send` (freedesktop notifications) |
| Open a URL or folder | `open` | `xdg-open` |
| Data folders | `~/Library/Application Support` | XDG folders: `~/.local/share/localsites`, `~/.config/localsites` |
| Menu bar icon | NSStatusItem | AppIndicator tray icon |
| App updates | Signed zip of the `.app` | Signed `.deb` and `.rpm` (the update manifest already lists files per OS and architecture) |
| Install hints | `brew install …` | Per-platform hints (`sudo apt install …`, `sudo dnf install …`) |

## Milestones

1. **Foundations.** Unit tests run on Ubuntu in CI. A small platform layer for paths, passwords, notifications,
   opening URLs and start at login. Install hints come from the platform instead of being hard-coded to Homebrew.
2. **Runtimes.** A runtime provider interface with two providers: managed downloads (PHP and MySQL) and the system
   (Homebrew on macOS, distro packages on Linux). Verified downloads with progress in the app.
3. **Domains and HTTPS.** systemd-resolved setup, binding ports 80 and 443, certificate trust for the system and
   browsers. Doctor checks for each.
4. **Desktop app.** Wails on WebKitGTK 4.1, tray icon, `.deb` and `.rpm` packages, signed updates.
5. **Release.** README, website and roadmap updates, then a Linux beta on the beta update channel.

The engine and CLI become usable on Linux after milestone 3, before the desktop app is done.

## Testing

- CI runs the unit tests on `ubuntu-latest` alongside macOS.
- End-to-end tests run in Ubuntu 24.04, Debian 12 and Fedora containers in CI.
- Before each release, a manual pass on a real Ubuntu desktop for DNS, certificates and the tray icon.
