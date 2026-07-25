// client.js — in-page hooks injected at the top of every proxied document.
// Running before the page's own scripts, it rewrites URLs at the source
// (fetch, XHR, WebSocket, and URL-bearing DOM attributes) so requests target
// the "/p/<token>" scheme, and registers the service worker as a safety net.
//
// Depends on codec.js and rewriter.js (loaded first). Best-effort by design:
// scripted cross-origin top-level navigations may still escape the proxy; the
// robust remainder is covered incrementally.
(function (self) {
  "use strict";
  var B = self.__bumshi;
  if (!B || !self.location) {
    return;
  }
  var origin = self.location.origin;

  function realBase() {
    return B.decodeProxied(self.location.pathname) || self.location.href;
  }

  function toProxy(url) {
    if (url == null || B.isNonNav(String(url))) {
      return url;
    }
    try {
      var abs = new URL(String(url), realBase());
      if (abs.protocol !== "http:" && abs.protocol !== "https:") {
        return url;
      }
      return origin + B.encodeAbsolute(abs.href);
    } catch (e) {
      return url;
    }
  }

  function toProxySrcset(value) {
    return String(value)
      .split(",")
      .map(function (part) {
        var f = part.trim().split(/\s+/);
        if (f.length === 0 || !f[0]) {
          return part;
        }
        f[0] = toProxy(f[0]);
        return f.join(" ");
      })
      .join(", ");
  }

  // --- fetch ---
  var _fetch = self.fetch;
  if (_fetch) {
    self.fetch = function (input, init) {
      try {
        if (typeof input === "string") {
          input = toProxy(input);
        } else if (input && input.url) {
          input = new Request(toProxy(input.url), input);
        }
      } catch (e) {
        /* ignore */
      }
      return _fetch.call(this, input, init);
    };
  }

  // --- XMLHttpRequest ---
  if (self.XMLHttpRequest && self.XMLHttpRequest.prototype.open) {
    var _open = self.XMLHttpRequest.prototype.open;
    self.XMLHttpRequest.prototype.open = function (method, url) {
      try {
        arguments[1] = toProxy(url);
      } catch (e) {
        /* ignore */
      }
      return _open.apply(this, arguments);
    };
  }

  // --- WebSocket ---
  var NativeWS = self.WebSocket;
  if (NativeWS) {
    var WrappedWS = function (url, protocols) {
      var target = url;
      try {
        var abs = new URL(String(url), realBase());
        var httpHref = abs.href.replace(/^ws:/, "http:").replace(/^wss:/, "https:");
        var proxied = origin + B.encodeAbsolute(httpHref);
        target = proxied.replace(/^http:/, "ws:").replace(/^https:/, "wss:");
      } catch (e) {
        /* ignore */
      }
      return protocols !== undefined ? new NativeWS(target, protocols) : new NativeWS(target);
    };
    WrappedWS.prototype = NativeWS.prototype;
    WrappedWS.CONNECTING = NativeWS.CONNECTING;
    WrappedWS.OPEN = NativeWS.OPEN;
    WrappedWS.CLOSING = NativeWS.CLOSING;
    WrappedWS.CLOSED = NativeWS.CLOSED;
    self.WebSocket = WrappedWS;
  }

  // --- DOM attributes ---
  var URL_ATTRS = { src: 1, href: 1, action: 1, poster: 1, formaction: 1, "data-src": 1 };

  if (self.Element && self.Element.prototype.setAttribute) {
    var _setAttribute = self.Element.prototype.setAttribute;
    self.Element.prototype.setAttribute = function (name, value) {
      try {
        var lower = String(name).toLowerCase();
        if (lower === "srcset") {
          value = toProxySrcset(value);
        } else if (URL_ATTRS[lower]) {
          value = toProxy(value);
        }
      } catch (e) {
        /* ignore */
      }
      return _setAttribute.call(this, name, value);
    };
  }

  function rewriteElement(el) {
    if (!el || el.nodeType !== 1 || typeof el.getAttribute !== "function") {
      return;
    }
    for (var attr in URL_ATTRS) {
      if (el.hasAttribute && el.hasAttribute(attr)) {
        var v = el.getAttribute(attr);
        var nv = toProxy(v);
        if (nv !== v) {
          el.setAttribute(attr, nv);
        }
      }
    }
    if (el.hasAttribute && el.hasAttribute("srcset")) {
      el.setAttribute("srcset", toProxySrcset(el.getAttribute("srcset")));
    }
  }

  if (self.MutationObserver && self.document) {
    var observer = new MutationObserver(function (mutations) {
      for (var i = 0; i < mutations.length; i++) {
        var m = mutations[i];
        if (m.type === "attributes") {
          rewriteElement(m.target);
        } else {
          for (var j = 0; j < m.addedNodes.length; j++) {
            var node = m.addedNodes[j];
            rewriteElement(node);
            if (node.querySelectorAll) {
              var kids = node.querySelectorAll("[src],[href],[action],[poster],[formaction],[srcset],[data-src]");
              for (var k = 0; k < kids.length; k++) {
                rewriteElement(kids[k]);
              }
            }
          }
        }
      }
    });
    var start = function () {
      observer.observe(self.document.documentElement, {
        childList: true,
        subtree: true,
        attributes: true,
        attributeFilter: ["src", "href", "action", "poster", "formaction", "srcset", "data-src"],
      });
    };
    if (self.document.documentElement) {
      start();
    } else {
      self.document.addEventListener("DOMContentLoaded", start);
    }
  }

  // --- register the service worker (safety net) ---
  if (self.navigator && self.navigator.serviceWorker) {
    try {
      self.navigator.serviceWorker.register("/__bumshi__/sw.js", { scope: "/" });
    } catch (e) {
      /* ignore */
    }
  }
})(typeof self !== "undefined" ? self : window);
