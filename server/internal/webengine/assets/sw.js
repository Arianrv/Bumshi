// sw.js — Bumshi service worker. It intercepts every request a proxied page
// makes and rewrites it to the "/p/<token>" scheme so subresources, XHR/fetch,
// and cross-origin loads all flow back through the proxy. It is the safety net
// behind the client-side hooks (client.js), catching anything they miss.
//
// Registered with scope "/" (the file is served with Service-Worker-Allowed: /).
/* global importScripts, clients */
"use strict";

importScripts("/__bumshi__/codec.js", "/__bumshi__/rewriter.js");
var B = self.__bumshi;

self.addEventListener("install", function () {
  self.skipWaiting();
});

self.addEventListener("activate", function (event) {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("fetch", function (event) {
  var url;
  try {
    url = new URL(event.request.url);
  } catch (e) {
    return;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    return;
  }
  event.respondWith(handle(event, url));
});

async function handle(event, url) {
  var proxyOrigin = self.location.origin;

  // Already-proxied requests and engine assets pass straight through. The check
  // decodes the token instead of matching the "/p/" prefix, so a target site's
  // own "/p/..." path is still rewritten rather than passed through broken.
  if (url.origin === proxyOrigin && B.isProxiedPath(url.pathname)) {
    return fetch(event.request);
  }

  var clientUrl = await clientURL(event);
  // The destination tells the rewriter whether relative references on this
  // request were resolved against the referring FILE (modules, workers) or
  // against the document. See the recovery block in rewriter.js.
  var target = B.rewriteRequestURL(proxyOrigin, clientUrl, url.href, event.request.destination);
  if (target === url.href) {
    return fetch(event.request);
  }
  // A top-level navigation is redirected rather than fetched: re-fetching it
  // would leave the address bar on the unproxied URL, and the replacement
  // Request would lose the navigate mode and its document destination (which
  // the server reads to decide whether to inject the runtime).
  if (event.request.mode === "navigate") {
    return Response.redirect(target, 302);
  }
  return fetch(await buildRequest(target, event.request));
}

// clientURL resolves the base a request's relative references belong to.
//
// The referrer comes first, and the document only after it, because they are
// not the same thing and the difference decides whether module-built sites work
// at all. A reference inside a stylesheet resolves against the stylesheet; a
// specifier inside an ES module resolves against that module. Both are the
// referrer. Only for a subresource of the page itself are the two identical,
// which is why using the document alone looked correct for so long.
//
// Telegram Web is the case that exposes it: index-<hash>.js does
// `import("./lang-<hash>.js")`, the browser resolves that against the module's
// own URL — a proxy URL — and asks for a path whose token is wreckage. Resolved
// against the document that is the wrong directory and 404s; resolved against
// the importing module it is exactly right.
//
// Only a referrer that decodes as a proxy URL is trusted, so an unrelated
// referrer can never redirect a request to the wrong site.
async function clientURL(event) {
  var ref = event.request.referrer;
  if (ref && ref !== "about:client") {
    try {
      if (new URL(ref).origin === self.location.origin && B.decodeProxied(new URL(ref).pathname)) {
        return ref;
      }
    } catch (e) {
      /* fall through to the document */
    }
  }
  var id = event.clientId || event.resultingClientId;
  if (id) {
    try {
      var c = await self.clients.get(id);
      if (c && c.url) {
        return c.url;
      }
    } catch (e) {
      /* ignore */
    }
  }
  if (ref) {
    return ref;
  }
  // Last resort: only when there is exactly one window, so the base is
  // unambiguous. With several tabs open, guessing the first one resolves a
  // request against a document it did not come from.
  try {
    var wins = await self.clients.matchAll({ type: "window" });
    if (wins && wins.length === 1) {
      return wins[0].url;
    }
  } catch (e) {
    /* ignore */
  }
  return null;
}

async function buildRequest(target, req) {
  var init = {
    method: req.method,
    headers: req.headers,
    credentials: "include",
    redirect: "follow",
    // "unsafe-url" keeps the full referrer PATH, and the path is the payload:
    // it is "/p/<token>", which the server decodes back into the real page the
    // request came from (see setRequestIdentity). A policy that trims the
    // referrer to its origin would leave only "https://<proxy>/", which decodes
    // to nothing.
    //
    // It is not unsafe here. The referrer never leaves this origin — the server
    // reads it, rewrites it into the target's own URL, and never forwards the
    // proxy's.
    //
    // This used to be "no-referrer", which stripped it entirely. Everything the
    // worker intercepts is most of the page, so most requests reached the
    // server with no referrer to decode: it then sent none upstream and
    // computed Sec-Fetch-Site: none — an assertion that the user typed the URL
    // into the address bar, attached to a subresource inside a document. That
    // combination cannot occur in a browser. It also breaks every CDN that
    // checks the referrer for hotlinking (images and video, mainly) and undoes
    // the sign-in fix for anything the worker handles.
    referrerPolicy: "unsafe-url",
  };
  // Only same-origin referrers may be set here, which is exactly what a proxied
  // document's URL is. Anything else is left to the default.
  if (req.referrer && req.referrer !== "about:client") {
    try {
      if (new URL(req.referrer).origin === self.location.origin) {
        init.referrer = req.referrer;
      }
    } catch (e) {
      /* ignore */
    }
  }
  if (req.method !== "GET" && req.method !== "HEAD") {
    // Buffer rather than stream the body. Request streams (body + duplex:
    // "half") are Chromium-only, so streaming here made every intercepted POST
    // throw on other engines.
    init.body = await req.clone().arrayBuffer();
  }
  return new Request(target, init);
}
