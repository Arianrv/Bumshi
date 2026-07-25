# Bumshi (بامشی)

Self-hosted, censorship-resistant web proxy and companion browser. You deploy a
small service on **your own VPS**; it fetches filtered sites server-side and
serves them to a companion browser app over **ordinary HTTPS** — no VPN, no
tunnel, nothing for DPI to fingerprint.

Bumshi is intended for **self-hosting or trusted-friend hosting only** — the
operator can see plaintext, so it is not an open public service.

> Status: early development. This repository currently contains **Step 0
> (foundations)**, **Step 1 (the control-plane service, `bumshid`)**,
> **Step 2 (the web-proxy engine)** — SSRF-safe streaming HTTP + WebSocket core,
> server-side HTML/CSS rewriting, and the browser runtime (service worker +
> client hooks) — **Step 3 (auth + admin panel + management CLI)**, and
> **Step 4 (installer + release/packaging automation)**. All features are off by
> default. Still to come: the YouTube/Telegram modules and the browser app.

## Install (self-host)

On a fresh Linux server (as root), the one-liner installs a checksum-verified
binary, a hardened systemd service, the management command, and optionally Caddy
for automatic HTTPS. Process substitution keeps the prompts interactive:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/Arianrv/Bumshi/main/install.sh)
# or, without curl:
bash <(wget -qO- https://raw.githubusercontent.com/Arianrv/Bumshi/main/install.sh)
```

It prompts for a domain, admin credentials (auto-generated if blank), and the
proxy/Cloudflare options — all with safe defaults, and all overridable via
`BUMSHI_*` environment variables for non-interactive installs. Re-running updates
the binary and keeps your config. Manage the service with `bumshi menu`, and
remove it with `uninstall.sh`.

**Docker:** a pinned, multi-arch image is published to
`ghcr.io/arianrv/bumshi/bumshid`. See `deploy/docker/` for a production
Compose file + Caddyfile.


## Repository layout

```
bumshi/
├── server/              # Go control-plane service (bumshid)
│   ├── cmd/bumshid/      # main entrypoint
│   ├── internal/         # config, logging, metrics, health, httpx, server, version
│   └── Dockerfile
├── Caddyfile            # TLS/edge configuration
├── docker-compose.yml   # local development stack (Caddy + bumshid)
├── Makefile             # developer tasks
└── .github/workflows/   # CI (build, test, lint, govulncheck, docker)
```

Later milestones add `engine/` (proxy engine), `modules/` (generic, YouTube,
Telegram), `app/` (the browser client), and `deploy/` (installer, systemd).

## The control-plane service (`bumshid`)

`bumshid` is the long-running service on the VPS. It sits behind Caddy (which
terminates public TLS on 443) and currently exposes:

| Method | Path        | Purpose                          |
| ------ | ----------- | -------------------------------- |
| GET    | `/healthz`  | Liveness probe (always 200)      |
| GET    | `/readyz`   | Readiness probe (200 / 503)      |
| GET    | `/version`  | Build information (JSON)          |
| GET    | `/metrics`  | Prometheus metrics (separate localhost listener) |
| ANY    | `/p/<enc>`  | Web proxy engine (when `BUMSHI_PROXY_ENABLED=true`) |

### Web proxy engine (`internal/proxy`)

Mounted under `/p/` when enabled. A target is encoded into the path
(`/p/<base64url(https://site/...)>`); the server fetches it and streams it back,
rewriting HTML/CSS URLs to keep navigation inside the proxy. Highlights that make
it a stronger base than typical ad-hoc engines:

- **SSRF-safe** — a dialer `Control` hook (`internal/proxy/ssrfguard`) refuses
  loopback/private/link-local/metadata IPs, defeating DNS-rebinding too.
- **Streaming** — non-text bodies pass through unbuffered (low TTFB, video, large
  files); only HTML/CSS is buffered (bounded) for rewriting.
- **Transparent WebSocket tunneling** — after relaying the handshake it pipes
  bytes both ways with no frame parsing, so any subprotocol works.
- **Privacy** — the client's IP and forwarding headers are never sent upstream.

Dynamic-JavaScript coverage (URLs built at runtime) is handled by the **browser
runtime** in `internal/webengine`: a service worker plus in-page hooks (fetch,
XHR, WebSocket, and URL-bearing DOM attributes) that rewrite requests to `/p/`.
The runtime is embedded in the binary, served under `/__bumshi__/`, and injected
into proxied HTML. Its shared codec/rewriter logic is unit-tested with Node
(`cd server/internal/webengine/assets && node --test`).

### Admin panel (`internal/admin`)

A deployer-only panel served under `BUMSHI_ADMIN_PATH` (default `/admin/`) when
`BUMSHI_ADMIN_ENABLED=true`. It provides a dark, 3x-ui-style UI for a dashboard,
runtime settings (toggle the proxy and access logging live), and access-user
management (create/delete end-user credentials and copy their `bumshi://`
connection links). Security: PBKDF2-HMAC-SHA256 password hashing, in-memory
sessions with `HttpOnly`/`Secure`/`SameSite=Strict` cookies, CSRF double-submit
tokens on mutations, and login rate limiting. It is **not** part of the client
app. Set the password with:

```bash
bumshid hash-password        # prints a hash for BUMSHI_ADMIN_PASSWORD_HASH
```

A systemd unit (`deploy/systemd/bumshi.service`) and a management menu
(`bumshi.sh`: start/stop/restart/status/logs/hash-password) are included for
self-hosted installs.

Design properties baked in from the start:

- **Zero third-party dependencies** — pure standard library, so it builds
  offline and has a minimal supply-chain surface.
- **Hardened HTTP server** — explicit read/read-header/write/idle timeouts,
  graceful shutdown, panic recovery, request IDs, security headers.
- **Privacy-first logging** — per-request access logging is **off by default**
  and gated behind `BUMSHI_ACCESS_LOG`; it is a development aid only and must
  stay off in public releases.
- **Localhost-bound by default** — the control plane and metrics listeners bind
  to `127.0.0.1`; only Caddy faces the internet.

## Requirements

- Go **1.22+** (to build/test the server)
- Docker (optional, for the container image and the dev stack)
- `golangci-lint` (optional, for `make lint`)

## Quick start (development)

```bash
# Run the server directly (development mode, access logging on)
make run

# In another terminal:
curl -s localhost:8080/healthz
curl -s localhost:8080/version
curl -s localhost:9090/metrics | head
```

Or run the full edge stack (Caddy in front of bumshid) via Docker:

```bash
docker compose up --build
curl -s localhost:8080/healthz   # through Caddy
```

## Common tasks

```bash
make build       # build ./bin/bumshid (version-stamped)
make test        # go test -race with coverage
make check       # fmt-check + vet + test (fast pre-commit gate)
make lint        # golangci-lint
make docker      # build the container image
make help        # list all targets
```

## Configuration

All settings come from `BUMSHI_*` environment variables with safe defaults; see
[`.env.example`](.env.example). Highlights:

| Variable               | Default          | Notes                                       |
| ---------------------- | ---------------- | ------------------------------------------- |
| `BUMSHI_ENV`           | `production`     | `development` enables text logs             |
| `BUMSHI_LISTEN_ADDR`   | `127.0.0.1:8080` | Control-plane listener (keep on localhost)   |
| `BUMSHI_METRICS_ADDR`  | `127.0.0.1:9090` | Metrics listener (never expose publicly)     |
| `BUMSHI_LOG_LEVEL`     | `info`           | `debug` \| `info` \| `warn` \| `error`      |
| `BUMSHI_ACCESS_LOG`    | `false`          | Dev aid only; keep false in production        |

## Module path

The Go module is `github.com/bumshi/bumshi/server`. If you host the project
under a different path, update `server/go.mod` and the import paths accordingly.

## License

GPL-3.0-or-later. See [`LICENSE`](LICENSE).
