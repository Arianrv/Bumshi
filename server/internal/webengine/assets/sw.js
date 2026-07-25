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

  // Already-proxied requests and engine assets pass straight through.
  if (
    url.origin === proxyOrigin &&
    (url.pathname.indexOf(B.PREFIX) === 0 || url.pathname.indexOf(B.ENGINE) === 0)
  ) {
    return fetch(event.request);
  }

  var clientUrl = await clientURL(event);
  var target = B.rewriteRequestURL(proxyOrigin, clientUrl, url.href);
  if (target === url.href) {
    return fetch(event.request);
  }
  return fetch(buildRequest(target, event.request));
}

async function clientURL(event) {
  if (event.clientId) {
    try {
      var c = await self.clients.get(event.clientId);
      if (c && c.url) {
        return c.url;
      }
    } catch (e) {
      /* ignore */
    }
  }
  if (event.request.referrer) {
    return event.request.referrer;
  }
  try {
    var wins = await self.clients.matchAll({ type: "window" });
    if (wins && wins.length) {
      return wins[0].url;
    }
  } catch (e) {
    /* ignore */
  }
  return null;
}

function buildRequest(target, req) {
  var init = {
    method: req.method,
    headers: req.headers,
    credentials: "include",
    redirect: "follow",
    referrerPolicy: "no-referrer",
  };
  if (req.method !== "GET" && req.method !== "HEAD") {
    init.body = req.body;
    init.duplex = "half";
  }
  return new Request(target, init);
}
