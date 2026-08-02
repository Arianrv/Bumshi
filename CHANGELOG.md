# Changelog

All notable changes to Bumshi are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- **WebSocket tunneling now works at all.** The tunnel hijacked the connection
  with a `w.(http.Hijacker)` type assertion, which can never succeed once the
  metrics middleware wraps the `ResponseWriter`, so every upgrade returned
  `500 streaming unsupported`. Hijacking now goes through
  `http.ResponseController`.
- **Long responses are no longer truncated.** The control plane's read/write
  timeouts were armed on every connection and survive `Hijack`, cutting video,
  large downloads and WebSocket tunnels off mid-stream. They are cleared on the
  proxy path, where cancellation comes from the client's request context.
- **Streamed responses are flushed as they arrive**, so server-sent events and
  long-polling are delivered instead of sitting in the output buffer.
- **GET form submissions keep their query string.** Only the encoded token was
  decoded, so `/p/<token>?q=…` lost the form data and every search box on every
  proxied site returned the unfiltered page.
- **Iframes inside proxied pages render again.** The service's own
  `X-Frame-Options: DENY` (and the other control-plane security headers) are no
  longer imposed on third-party content; the upstream's own values are used.
- **HTML rewriting is markup-aware.** A regex sweep could not tell markup from
  text and so rewrote inline JavaScript (`el.href = "/x"`), corrupted escaped
  JSON (`href=\"`), mangled unrelated attributes (`data-action`,
  `data-track-src`), and rewrote `url()` inside script bodies. A proper scanner
  now rewrites only real start tags and CSS, decodes character references before
  resolving, re-escapes values for their quoting context, and parses `srcset`
  per spec so `data:` URIs survive.
- **`<base href>` is honoured instead of broken.** It is now resolved and used
  as the document's resolution base, and the attribute is dropped rather than
  rewritten into a proxy path that broke every relative reference.
- **`<meta http-equiv="refresh">` is rewritten**, so timed redirects stay inside
  the proxy instead of navigating straight out of it.

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
