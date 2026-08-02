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
- **Oversized documents are no longer truncated.** A text body past the rewrite
  budget was silently cut off mid-tag; it is now served whole and unrewritten,
  with the runtime bootstrap still injected so the service worker covers it.
- **Subresource Integrity no longer blocks proxied stylesheets.** `integrity` is
  dropped from `<link>` elements whose CSS the proxy rewrites, and kept
  everywhere else — script bodies are never modified, so their hashes still hold.
- **The runtime bootstrap is only injected into documents**, not into HTML
  fragments a page fetches over XHR (it honours `Sec-Fetch-Dest`), and the
  `<head>` search no longer mistakes `<header>` for the document head.
- **A target site's own `/p/…` paths are proxied again.** The client runtime
  matched on the path prefix, so a real site's `/p/product/123` looked
  already-proxied and escaped; it now decodes and validates the token.
- **Service worker fixes**: top-level navigations are redirected rather than
  re-fetched (keeping the address bar and the document destination correct), the
  document base is no longer guessed from an unrelated tab when several are
  open, and intercepted POST bodies are buffered instead of streamed — request
  streams are Chromium-only and threw everywhere else.
- The proxy refuses to proxy its own public URL even when an intermediary
  rewrites the Host header, and no longer nil-derefs when built without metrics.
- **Sign-in works again on sites that check request provenance.** `Origin` and
  `Referer` were stripped outright, which Django (and most CSRF middleware)
  rejects on any POST over HTTPS. Both are now rebuilt to describe the target,
  which is exactly what a browser talking to that site directly would send.

### Security
- **Cookies are namespaced per site.** Every proxied site shared one browser
  origin, so the browser handed each site every other site's cookies and the
  proxy forwarded them upstream — a user signed in to Google had their session
  cookie sent to every site they visited afterwards. Cookies are now stored
  under a name encoding the scope they belong to and unpacked per request, with
  `Domain` semantics preserved so sign-in still works across subdomains. The
  jar stays on the user's own device: nothing about their sessions is stored on
  the server.
- **The access token can no longer reach a target site.** The upstream `Cookie`
  header is rebuilt from the namespaced jar, so anything unrecognised — the
  `bumshi_access` token above all — is left behind. This holds on the WebSocket
  path too, and no longer depends on `BUMSHI_PROXY_REQUIRE_TOKEN` being on.
- An upstream site can no longer shadow Bumshi's own cookies, because upstream
  `Set-Cookie` names are namespaced and unprefixed cookies are never forwarded.
- **The admin panel has its own listener and is no longer on the proxy origin.**
  Sharing an origin with proxied content meant any site a user opened through
  the proxy could call the panel's API with the deployer's session cookie
  attached — SameSite offers nothing there, because it is literally the same
  site. It now binds to `127.0.0.1:8081` by default (`BUMSHI_ADMIN_ADDR`) and is
  reached over an SSH tunnel, or given a hostname of its own.
- **Content-Security-Policy is translated instead of discarded.** Dropping it
  left one XSS anywhere able to compromise every site a user browses through the
  shared origin. Host sources now collapse to `'self'` while keywords, nonces
  and hashes survive, and the injected runtime is admitted with a per-response
  nonce. Under a shared origin `'self'` is weaker than the site intended; what
  survives is the non-origin half of CSP, which is most of what stops XSS.
- **Web storage is namespaced per site.** `localStorage`, `sessionStorage`,
  IndexedDB and CacheStorage were shared across every proxied site, so two sites
  using the same key or database name silently overwrote each other. Like the
  cookie shim this fixes collisions and casual cross-reads; it is not a boundary
  against a hostile page on the shared origin.
- **The desktop client can authenticate.** It has no cookie API, so it could
  never send its access token and was locked out whenever
  `BUMSHI_PROXY_REQUIRE_TOKEN` was on. A new `/__bumshi__/auth` endpoint installs
  the token as an HttpOnly cookie; the access log redacts that path so the token
  never reaches disk.

### Admin panel, privacy and configuration
- **The service refuses to start as an open relay.** An enabled proxy with
  `PROXY_REQUIRE_TOKEN` off lets anyone who learns the domain route traffic
  through the operator's IP, and that was the installer's default. Token
  enforcement is now the shipped default, and starting without it requires
  `BUMSHI_PROXY_ALLOW_OPEN_RELAY=true` to say so deliberately.
- **Configuration no longer fails open.** A malformed `BUMSHI_*` value silently
  fell back to its default, so `PROXY_REQUIRE_TOKEN=ture` read as `false` and
  disabled a security control without a word. Parse errors are collected and
  reported, and the service refuses to start.
- **Access logging no longer records a browsing history.** With logging on, every
  request wrote `/p/<base64url(target)>` — the full URL of the page, decodable in
  one command. The proxy prefix survives for rate and error analysis; the target
  does not.
- **Login rate limiting sees the real client.** Keyed on `RemoteAddr`, which
  behind a reverse proxy is always the proxy, it was a single shared bucket:
  any attacker could exhaust it and lock the operator out of their own panel.
  Forwarding headers are now read, but only from a trusted (loopback) peer,
  since otherwise a client could mint a fresh identity per request.
- **A malformed admin password hash is reported instead of swallowed**, at
  startup rather than as "invalid credentials" on every login attempt, and the
  panel no longer generates a password and prints it into the logs, where it
  outlives the session it was meant for.
- **Panel settings persist.** Switching the proxy off found it back on after any
  restart — a safety switch that silently undid itself.
- `PUBLIC_URL` is validated and required when the panel is enabled (it was
  producing connection links that every client silently refused), an empty
  `ACCESS_STORE_PATH` now really does disable persistence, cookie `Secure`
  follows the request's actual transport rather than the environment's name,
  logout takes the CSRF check like every other mutation, unknown admin paths
  404 instead of serving the app shell, the settings API rejects unknown fields
  and modules, and the access-user listing no longer hands every token to the
  browser on page load.

### Android app
- **A web page can no longer take over the proxy.** `bumshi://connect#…` links
  were applied silently, and the intent filter is exported and BROWSABLE — so
  any page could repoint every request the user makes through a server of its
  choosing, invisibly. Connecting now requires an explicit confirmation naming
  the domain being trusted.
- **Connection links are validated.** The `url` field had to be a bare hostname
  and was not checked, so `evil.com/path?x=` or `real.com@evil.com` were
  accepted and silently redirected all traffic. Hostnames, token shape and label
  are now validated, and a stored instance that fails today's rules is dropped.
- **Private ("Hiss") tabs no longer wipe the whole cookie jar.** Closing one
  called `removeSessionCookies(null)`, signing the user out of every site in
  every normal tab while leaving the private tab's own persistent cookies
  behind — wrong in both directions. The app now deletes exactly the cookies of
  the sites that tab visited, using the same scope names the proxy assigns.
  Storage still cannot be isolated per tab (WebView has one data directory per
  process), and the strings say so rather than promising incognito.
- **Camera and microphone ask every time.** After the first OS grant, any page
  was given the camera or mic silently — and because all proxied sites share one
  origin, WebView's own per-origin prompt cannot tell them apart either. The app
  prompts against the real site.
- **The app can no longer be made permanently unlaunchable.** A keystore reset
  or restore-from-backup made `EncryptedSharedPreferences` throw out of
  `onCreate`, with no recovery short of reinstalling. Storage failures are now
  contained: the unreadable store is discarded and recreated, and failing that
  the browser still starts.
- **Plaintext HTTP is refused** (`usesCleartextTraffic=false` plus a network
  security config), and the WebView no longer loads mixed content.
- **External links work**: `intent:`, `tel:`, `mailto:` and `market:` used to
  dead-end on an error page. `intent:` URLs are stripped of their component,
  package and selector before launching — a page must not be able to reach a
  component the user did not choose — with `browser_fallback_url` honoured.
- Downloads are named after the real URL rather than the base64 proxy token, and
  a private tab's downloads stay out of the shared Downloads folder.
- History writes moved off the UI thread (they re-serialised up to 500 entries
  on every page load), background tabs are paused when the app is, the tab
  switcher no longer acts on stale indices, and toggling the proxy on a blank
  tab no longer runs a web search for "about:blank".
- **Release builds cannot be debug-signed.** The build fell back to the debug
  key when no keystore was present, which ships a key whose password is public
  and produces an APK no properly signed build can ever update. R8 is enabled
  and `versionCode` comes from the build invocation instead of being pinned
  at 1.

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
