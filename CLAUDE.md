# Working in this repository

Notes for anyone — human or AI assistant — making changes here. Read this before
touching files or running git.

## What is public and what is not

This repository is **public on GitHub**. These directories are deliberately
excluded and must never be committed, staged, or `git add -f`'d:

| Path | What it is |
| ---- | ---------- |
| `/android/` | The Android browser app. Private source; distributed as a prebuilt APK/AAB only. |
| `/app/` | The desktop (Tauri) browser app. Private source; distributed as prebuilt binaries only. |
| `/img/` | Raw brand assets. |
| `DESIGN.md` | Private design and planning document. |

They are listed in `.gitignore`. Do not remove those entries, do not add
exceptions to them, and **do not use `git add -A` as a way of "including
everything"** without checking what it picked up.

Consequences worth understanding:

- **Changes to `/android/` and `/app/` never reach GitHub or CI.** Nothing in
  those directories is compiled, linted, or tested by any automated system. They
  are verified by review alone, so they need a local build
  (`cd android && ./gradlew testDebugUnitTest`) before they can be trusted.
- A commit that touches only those directories will appear to contain nothing.
  That is expected, not a bug.

Before pushing anything that touched them:

```bash
git status --short                 # nothing under android/, app/, img/
git diff --cached --name-only      # confirm what is actually staged
```

## Verification expectations

There is no Go toolchain assumption in this document — CI provides it. What
matters is that these are the gates, and they are not optional:

- `gofmt -s -l .` must print nothing. Hand-editing struct or map literals is
  where this usually breaks, because gofmt aligns values to the longest key.
- `go vet ./...`, `go test -race ./...`, `go build ./...`
- `golangci-lint` runs v2 with the schema in `.golangci.yml`. The version is
  pinned in `.github/workflows/ci.yml`; leaving it unpinned lets a major
  upgrade silently invalidate the config.
- `cd server && node --test` covers the browser runtime and admin assets.

**When changing a function signature, search `_test.go` files too.** Excluding
them from a grep is how a signature change compiles locally and then fails CI
with a type error in a test literal.

## Three copies of one algorithm

The cookie-scope hash (FNV-1a 64) exists in three places and they must agree
byte for byte:

- `server/internal/proxy/cookies.go`
- `server/internal/webengine/assets/rewriter.js`
- `android/app/src/main/java/com/bumshi/browser/CookieScopes.kt`

If they ever drift, every cookie already stored in every user's browser becomes
unreadable at once, and every signed-in session breaks simultaneously. The same
test vectors appear in all three test suites on purpose — change them together
or not at all.

**`cookieScope` has the same requirement**, and it is easier to get wrong because
the two copies are reached by different paths:

- `server/internal/proxy/cookies.go` scopes an upstream `Set-Cookie`
- `server/internal/webengine/assets/rewriter.js` scopes a `document.cookie` write

Both must choose the same name for the same logical cookie. When they disagree —
the shim once dropped `Domain=` and always wrote host-only, while the server
honoured it — the jar holds two copies under two scopes, `unpackCookies` matches
both, and the upstream request carries `Cookie: NID=<server>; NID=<script>`.
Sites that sign or pin a cookie read that as tampering. On Google it is an
immediate "unusual traffic" interstitial that **cannot be cleared**, because
solving the challenge rewrites only the server's copy and leaves the other in
place. Fixing the code does not fix an already-poisoned jar: the stale cookie
stays until its own expiry, so clearing browser storage is part of the fix.

## Things that look like bugs and are not

- The proxy deliberately does **not** impose the service's own security headers
  on proxied content (`X-Frame-Options`, nosniff, and friends). Doing so blocks
  legitimate iframes and breaks sites that serve CSS with a sloppy Content-Type.
- `Origin`, `Referer` and `Sec-Fetch-Site` are **rebuilt** to describe the
  target, not forwarded. They must stay consistent with each other: a real
  `Referer` next to a browser-computed `Sec-Fetch-Site` produces combinations no
  browser can emit, which anti-abuse systems detect.
- The admin panel binds to localhost on its own listener. That is not an
  oversight — sharing an origin with proxied content lets any site a user opens
  call the panel's API with the deployer's session attached.
