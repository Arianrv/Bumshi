// rewriter.js — pure URL-rewriting decisions shared by the service worker and
// the client hooks. Mirrors the Go rewriter/link semantics so both sides agree.
// Attaches to self.__bumshi; depends on codec.js being loaded first.
(function (self) {
  "use strict";
  var B = (self.__bumshi = self.__bumshi || {});

  var PREFIX = "/p/";
  var ENGINE = "/__bumshi__/";
  var NON_NAV = ["#", "data:", "blob:", "mailto:", "tel:", "javascript:", "about:", "ws:", "wss:"];

  function isNonNav(ref) {
    var s = String(ref).trim().toLowerCase();
    for (var i = 0; i < NON_NAV.length; i++) {
      if (s.indexOf(NON_NAV[i]) === 0) {
        return true;
      }
    }
    return false;
  }

  // decodeProxied returns the absolute target string for a "/p/<token>" path, or
  // null if the path is not a proxied path.
  function decodeProxied(pathname) {
    if (String(pathname).indexOf(PREFIX) !== 0) {
      return null;
    }
    var token = pathname.slice(PREFIX.length);
    var slash = token.indexOf("/");
    if (slash >= 0) {
      token = token.slice(0, slash);
    }
    if (!token) {
      return null;
    }
    try {
      return B.decode(token);
    } catch (e) {
      return null;
    }
  }

  function encodeAbsolute(abs) {
    return PREFIX + B.encode(abs);
  }

  // resolve resolves ref against baseAbs and returns the proxy path, leaving
  // non-navigational references (data:, javascript:, #, ...) untouched.
  function resolve(baseAbs, ref) {
    if (ref == null) {
      return ref;
    }
    var t = String(ref).trim();
    if (t === "" || isNonNav(t)) {
      return ref;
    }
    var abs;
    try {
      abs = new URL(t, baseAbs);
    } catch (e) {
      return ref;
    }
    if (abs.protocol !== "http:" && abs.protocol !== "https:") {
      return ref;
    }
    return encodeAbsolute(abs.href);
  }

  // rewriteRequestURL maps a request the browser is about to make into a proxied
  // URL on proxyOrigin, using clientUrl (the proxied document's URL) as the base
  // for same-origin references. Requests that are already proxied or target the
  // engine assets are returned unchanged.
  function rewriteRequestURL(proxyOrigin, clientUrl, requestUrl) {
    var req;
    try {
      req = new URL(requestUrl);
    } catch (e) {
      return requestUrl;
    }
    if (req.origin === proxyOrigin) {
      if (req.pathname.indexOf(PREFIX) === 0 || req.pathname.indexOf(ENGINE) === 0) {
        return requestUrl;
      }
      var baseAbs = clientUrl ? decodeProxied(new URL(clientUrl).pathname) : null;
      if (!baseAbs) {
        return requestUrl;
      }
      var real;
      try {
        real = new URL(req.pathname + req.search + req.hash, baseAbs).href;
      } catch (e) {
        return requestUrl;
      }
      return proxyOrigin + PREFIX + B.encode(real);
    }
    // Cross-origin absolute request: proxy it directly.
    return proxyOrigin + PREFIX + B.encode(req.href);
  }

  B.PREFIX = PREFIX;
  B.ENGINE = ENGINE;
  B.isNonNav = isNonNav;
  B.decodeProxied = decodeProxied;
  B.encodeAbsolute = encodeAbsolute;
  B.resolve = resolve;
  B.rewriteRequestURL = rewriteRequestURL;
})(typeof self !== "undefined" ? self : globalThis);
