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
  // null if the path is not one of ours.
  //
  // The decoded token must be a real http(s) URL. Without that check any path
  // that merely starts with "/p/" looks proxied — and real sites do have them
  // ("/p/product/123" is a common product-page shape). Those were short-
  // circuited as already-proxied and escaped the proxy entirely.
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
    var decoded;
    try {
      decoded = B.decode(token);
    } catch (e) {
      return null;
    }
    var u;
    try {
      u = new URL(decoded);
    } catch (e) {
      return null;
    }
    return u.protocol === "http:" || u.protocol === "https:" ? decoded : null;
  }

  // isProxiedPath reports whether a path is one the proxy already serves: a
  // valid "/p/<token>" or an engine asset. Everything else — including a target
  // site's own "/p/..." path — still needs rewriting.
  function isProxiedPath(pathname) {
    var p = String(pathname);
    return p.indexOf(ENGINE) === 0 || decodeProxied(p) !== null;
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
  // moduleLike marks the request destinations whose relative references the
  // browser resolves against the REFERRING FILE rather than the document, and
  // which therefore produce recoverable wreckage under PREFIX. See the
  // recovery block in rewriteRequestURL.
  //
  // Gating on this is what keeps the recovery from eating a target site's own
  // "/p/..." path. Those arrive as documents, images or plain fetches, never as
  // module scripts, and a server-rendered link to one was already rewritten
  // during the HTML scan and never reaches the worker at all.
  var moduleLike = { script: 1, worker: 1, sharedworker: 1, serviceworker: 1 };

  function rewriteRequestURL(proxyOrigin, clientUrl, requestUrl, destination) {
    var req;
    try {
      req = new URL(requestUrl);
    } catch (e) {
      return requestUrl;
    }
    if (req.origin === proxyOrigin) {
      if (isProxiedPath(req.pathname)) {
        return requestUrl;
      }
      var baseAbs = clientUrl ? decodeProxied(new URL(clientUrl).pathname) : null;
      if (!baseAbs) {
        return requestUrl;
      }
      // A module path under PREFIX whose token does NOT decode is the wreckage
      // of the browser resolving a relative specifier against a proxy URL. It
      // is not corrupt — it is recoverable, because the resolution was purely
      // mechanical:
      //
      //   module   /p/<token of https://web.telegram.org/k/index-abc.js>
      //   import("./lang-xyz.js")
      //   browser  /p/lang-xyz.js          <- last segment replaced
      //
      // Everything after PREFIX is therefore the original specifier, already
      // resolved against the importing file's directory. Treating it as
      // RELATIVE — no leading slash — and resolving it against that file's real
      // URL reconstructs the target exactly. Left as the root-relative
      // "/p/lang-xyz.js" it resolves to the site root and 404s, which is why
      // every ES-module site rendered a blank page.
      //
      // Restricted to module-like destinations because a target site's own
      // "/p/product/123" is indistinguishable by shape. That one arrives as a
      // document, an image or a plain fetch; only the wreckage arrives as a
      // script, and only when the base is the importing file.
      var ref = req.pathname;
      if (moduleLike[destination] && ref.indexOf(PREFIX) === 0) {
        ref = ref.slice(PREFIX.length);
      }
      var real;
      try {
        real = new URL(ref + req.search + req.hash, baseAbs).href;
      } catch (e) {
        return requestUrl;
      }
      return proxyOrigin + PREFIX + B.encode(real);
    }
    // Cross-origin absolute request: proxy it directly.
    return proxyOrigin + PREFIX + B.encode(req.href);
  }

  // --- cookie namespacing ---
  //
  // The server stores each site's cookies in the shared browser jar under a name
  // that encodes the scope they belong to (see server/internal/proxy/cookies.go).
  // These helpers mirror that scheme so page scripts still see their own cookies
  // through document.cookie. The hash MUST stay byte-identical to the Go one: if
  // the two ever disagree, every stored cookie becomes unreadable at once.
  var COOKIE_PREFIX = "b_";
  var FNV_OFFSET = BigInt("14695981039346656037");
  var FNV_PRIME = BigInt("1099511628211");
  var U64 = (BigInt(1) << BigInt(64)) - BigInt(1);

  function fnv1a64(str) {
    var bytes = new TextEncoder().encode(String(str).toLowerCase());
    var h = FNV_OFFSET;
    for (var i = 0; i < bytes.length; i++) {
      h = (h ^ BigInt(bytes[i])) & U64;
      h = (h * FNV_PRIME) & U64;
    }
    return h.toString(16);
  }

  function cookiePrefix(scope, domainMatch) {
    return COOKIE_PREFIX + fnv1a64(scope) + (domainMatch ? "d" : "h") + "_";
  }

  // scopePrefixes lists every prefix whose cookies belong to host: the host-only
  // one, plus a domain-scoped one for the host and each parent of two or more
  // labels.
  function scopePrefixes(host) {
    var h = String(host).toLowerCase().replace(/\.$/, "");
    if (!h) {
      return [];
    }
    var out = [cookiePrefix(h, false), cookiePrefix(h, true)];
    var rest = h;
    for (;;) {
      var dot = rest.indexOf(".");
      if (dot < 0) {
        break;
      }
      rest = rest.slice(dot + 1);
      if (rest.indexOf(".") < 0) {
        break;
      }
      out.push(cookiePrefix(rest, true));
    }
    return out;
  }

  // cookieScope decides which scope a cookie belongs to, mirroring cookieScope in
  // server/internal/proxy/cookies.go exactly. A page setting a cookie through
  // document.cookie must land on the same name the server would have chosen for
  // the equivalent Set-Cookie, or the jar ends up holding two copies of one
  // cookie under two scopes and both are unpacked onto the same upstream
  // request.
  //
  // A Domain is honoured only when the request host is inside it and it has at
  // least two labels, so a page cannot claim a cookie for a whole suffix.
  function cookieScope(domainAttr, requestHost) {
    var host = String(requestHost == null ? "" : requestHost)
      .toLowerCase()
      .replace(/\.$/, "");
    var d = String(domainAttr == null ? "" : domainAttr)
      .trim()
      .toLowerCase()
      .replace(/^\./, "")
      .replace(/\.$/, "");
    if (!d || d.indexOf(".") < 0) {
      return { scope: host, domainMatch: false };
    }
    // d === host, or host is a subdomain of d.
    if (("." + host).slice(-(d.length + 1)) !== "." + d) {
      return { scope: host, domainMatch: false };
    }
    return { scope: d, domainMatch: true };
  }

  B.cookiePrefix = cookiePrefix;
  B.scopePrefixes = scopePrefixes;
  B.cookieScope = cookieScope;

  B.PREFIX = PREFIX;
  B.ENGINE = ENGINE;
  B.isNonNav = isNonNav;
  B.decodeProxied = decodeProxied;
  B.isProxiedPath = isProxiedPath;
  B.encodeAbsolute = encodeAbsolute;
  B.resolve = resolve;
  B.rewriteRequestURL = rewriteRequestURL;
})(typeof self !== "undefined" ? self : globalThis);
