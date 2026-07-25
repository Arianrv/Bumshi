# Changelog

All notable changes to Bumshi are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Control plane (`bumshid`)**: hardened HTTP server with timeouts, graceful
  shutdown, request IDs, panic recovery, security headers, health/readiness,
  Prometheus metrics, and privacy-gated access logging.
- **Web proxy engine**: SSRF-safe streaming HTTP forwarder, transparent
  WebSocket tunnel, server-side HTML/CSS rewriting, and an embedded browser
  runtime (service worker + client hooks) that routes dynamic requests.
- **Admin panel**: session auth (PBKDF2-HMAC-SHA256), CSRF, login rate limiting,
  a dark/light 3x-ui-style UI with English + Persian (RTL) i18n, live settings,
  and access-user management with `bumshi://` connection links.
- **Tooling**: `bumshid hash-password` / `version` subcommands, a hardened
  systemd unit, and a `bumshi` management command (start/stop/logs/rotate-domain
  /update).
- **Install & release**: interactive `install.sh` (curl/wget, checksum-verified)
  and `uninstall.sh`; a tagged release workflow producing multi-arch binaries
  with checksums and build-provenance attestation, plus a multi-arch container
  image on GHCR; production Docker Compose + Caddyfile.

[Unreleased]: https://github.com/Arianrv/Bumshi/commits/main
