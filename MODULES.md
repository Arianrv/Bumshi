# Site modules

The built-in web proxy engine handles most sites generically. Two heavy,
JavaScript-dense apps benefit from dedicated handling: **Telegram** and
**YouTube**. This document covers how each works with Bumshi.

## Telegram — works through the generic engine

Telegram's web client (`web.telegram.org`) is a normal web app over HTTPS +
**WebSocket**, and the Bumshi engine already carries both:

- the generic proxy fetches the page server-side under `/p/<token>`;
- the embedded runtime rewrites its dynamic requests;
- the WebSocket connection to Telegram's data centers is tunneled transparently
  by the engine (no frame parsing).

**How to use it:** in Bumshi Browser, turn proxy mode on and open
`https://web.telegram.org`. Login works normally — the code arrives out-of-band
(SMS or another device), and Telegram sees the VPS IP, not the user's.

**Caveats (test these):**
- Telegram Web is a heavy, stateful SPA — validate the full login + messaging
  flow on your instance; expect occasional friction that the generic rewriter
  may need help with.
- **Voice/video calls won't work** (WebRTC/UDP can't traverse a content proxy).
  Text, media, and channels do.

No extra service to deploy — it rides on the engine you already run.

## YouTube — use a dedicated front-end (Invidious or Piped)

Generic proxies choke on YouTube (aggressive anti-bot, DASH video, heavy JS), so
the practical approach is a purpose-built front-end that proxies YouTube's API
**and** video through your server:

- **[Invidious](https://github.com/iv-org/invidious)** — lightweight, self-hosted
  (recommended starting point).
- **[Piped](https://github.com/TeamPiped/Piped)** — more components (backend +
  frontend + proxy), heavier.

### Deploy Invidious next to Bumshi

Run Invidious via its **official `docker-compose`** (it needs a Postgres DB and a
generated `hmac_key`), then expose it on a subdomain of your Bumshi domain and
route to it with Caddy:

```caddyfile
# Add to your Caddyfile, alongside the main Bumshi site block.
yt.example.com {
	encode gzip
	header -Server
	reverse_proxy invidious:3000
}
```

Point users at `https://yt.example.com` for YouTube. Because it lives on your
own domain over normal HTTPS, it fits the same DPI-resistance model as the rest
of Bumshi.

### Bandwidth (the real constraint)

Video bytes still cross the international link. On slow connections:

- **Lower the default quality** in Invidious/Piped settings.
- **Optional server-side transcoding/downscaling** (`yt-dlp` + `ffmpeg`) can cut
  bitrate further so playback is smooth — a follow-up enhancement.
- **Caching pays off here**, unlike unique-file traffic: YouTube content is
  shared, so a popular video is fetched once and served to many — and a domestic
  cache node (if you run one) amplifies that.

## Enabling in the panel

The admin panel exposes `youtube` and `telegram` toggles (currently placeholders
in `settings`). Telegram needs no backend change to work through the engine;
YouTube's toggle is informational until a deployed Invidious/Piped endpoint is
wired in — set `yt.<domain>` up as above and share it with your users.
