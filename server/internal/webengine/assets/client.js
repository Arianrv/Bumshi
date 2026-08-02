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
    var raw = String(url);
    // Already-proxied references must never be wrapped again, or they nest into
    // "/p/<enc(/p/<enc(...)>)>" loops. The server injects ROOT-RELATIVE proxy
    // links (e.g. "/p/<token>"); those belong to the proxy origin and must NOT
    // be resolved against realBase() (the real site), so short-circuit before
    // resolving. isProxiedPath decodes the token rather than matching the
    // prefix, so a target site's own "/p/product/123" is still proxied.
    if (B.isProxiedPath(raw)) {
      return url;
    }
    try {
      var abs = new URL(raw, realBase());
      if (abs.protocol !== "http:" && abs.protocol !== "https:") {
        return url;
      }
      // Absolute proxy URLs already on our own origin: leave untouched too.
      if (abs.origin === origin && B.isProxiedPath(abs.pathname)) {
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

  // --- document.cookie ---
  //
  // Cookies live in the shared proxy-origin jar under scope-encoded names, so a
  // page reading document.cookie raw would see every site's cookies wearing
  // unrecognisable names, and a page WRITING one would store it unscoped, where
  // the server drops it. This shim presents each page only its own cookies,
  // under their real names.
  //
  // It is a compatibility layer, not a security boundary: a hostile page on this
  // origin can still reach the unpatched accessor through a fresh iframe. The
  // boundary that does hold is server-side — the proxy will not send one site's
  // cookies to another, whatever the page does here.
  (function shimCookies() {
    if (!self.document || !B.scopePrefixes) {
      return;
    }
    var proto = self.Document && self.Document.prototype;
    var native = proto && Object.getOwnPropertyDescriptor(proto, "cookie");
    if (!native || !native.get || !native.set) {
      return;
    }

    function currentHost() {
      try {
        return new URL(realBase()).hostname;
      } catch (e) {
        return "";
      }
    }

    function strip(name, prefixes) {
      for (var i = 0; i < prefixes.length; i++) {
        if (name.indexOf(prefixes[i]) === 0 && name.length > prefixes[i].length) {
          return name.slice(prefixes[i].length);
        }
      }
      return null;
    }

    try {
      Object.defineProperty(self.document, "cookie", {
        configurable: true,
        get: function () {
          var host = currentHost();
          if (!host) {
            return "";
          }
          var prefixes = B.scopePrefixes(host);
          var out = [];
          var raw = String(native.get.call(self.document) || "");
          raw.split(";").forEach(function (pair) {
            var eq = pair.indexOf("=");
            if (eq < 0) {
              return;
            }
            var name = strip(pair.slice(0, eq).trim(), prefixes);
            if (name !== null) {
              out.push(name + "=" + pair.slice(eq + 1));
            }
          });
          return out.join("; ");
        },
        set: function (value) {
          var host = currentHost();
          var raw = String(value);
          var eq = raw.indexOf("=");
          if (!host || eq < 0) {
            return;
          }
          // Split off the value (always first) from the attributes, pulling out
          // Domain= and discarding Path= — everything is stored at Path=/ on the
          // shared origin.
          var parts = raw.slice(eq + 1).split(";");
          var keep = [parts[0]];
          var domain = "";
          for (var i = 1; i < parts.length; i++) {
            var m = /^\s*domain\s*=(.*)$/i.exec(parts[i]);
            if (m) {
              domain = m[1].trim();
              continue;
            }
            if (/^\s*path\s*=/i.test(parts[i])) {
              continue;
            }
            keep.push(parts[i]);
          }
          // Scope it exactly as the server scopes an equivalent Set-Cookie.
          // Dropping Domain= here instead — which this did — stores a second,
          // host-only copy of a cookie the server already stored domain-scoped.
          // Both then match on the way out and the upstream request carries
          // "Cookie: NID=<server>; NID=<script>". A site that signs or pins that
          // cookie reads the conflict as tampering: on Google it is an immediate
          // and unclearable "unusual traffic" interstitial, because solving the
          // challenge only rewrites the server's copy and leaves the other.
          var sc = B.cookieScope(domain, host);
          var name = B.cookiePrefix(sc.scope, sc.domainMatch) + raw.slice(0, eq).trim();
          native.set.call(self.document, name + "=" + keep.join(";") + "; Path=/");
        },
      });
    } catch (e) {
      /* a browser that will not let us redefine it keeps the native behaviour */
    }
  })();

  // --- web storage ---
  //
  // localStorage, sessionStorage, IndexedDB and CacheStorage are keyed by
  // origin, and every proxied site now shares one. Two sites that both use the
  // key "token" or a database called "app" silently overwrite each other — a
  // correctness bug before it is a privacy one — and any page can read what the
  // others stored.
  //
  // Every name is therefore prefixed with the current target's scope. Like the
  // cookie shim this is a compatibility and collision fix, not a boundary: a
  // hostile page on this origin can still reach the unwrapped APIs through a
  // fresh iframe.
  (function shimStorage() {
    if (!B.cookiePrefix) {
      return;
    }

    function scope() {
      try {
        return B.cookiePrefix(new URL(realBase()).hostname, false);
      } catch (e) {
        return null;
      }
    }

    function wrapStorage(name) {
      var native;
      try {
        native = self[name];
        if (!native) {
          return;
        }
      } catch (e) {
        return; // storage disabled by policy
      }
      // A plain object carrying getItem/setItem is not a usable stand-in for
      // Storage. Most real code reaches keys as properties — `store.token`,
      // `store["token"] = v`, `delete store.token`, `Object.keys(store)`,
      // `for (k in store)` — and a facade drops every one of them silently: the
      // write lands on the facade and is gone at the next reload, while
      // Object.keys reports the facade's own method names instead of the stored
      // keys, so anything that enumerates its storage reads back "getItem" and
      // "setItem" as though they were data.
      //
      // A Proxy over the real Storage keeps the entire interface, including the
      // prototype chain: `localStorage instanceof Storage` stays true and
      // Object.prototype.toString still reports [object Storage]. That matters
      // past correctness — an object failing those checks is a glaring anomaly
      // to any script that inspects its environment.
      var methods = {
        getItem: function (k) {
          var p = scope();
          return p ? native.getItem(p + String(k)) : null;
        },
        setItem: function (k, v) {
          var p = scope();
          if (p) {
            native.setItem(p + String(k), String(v));
          }
        },
        removeItem: function (k) {
          var p = scope();
          if (p) {
            native.removeItem(p + String(k));
          }
        },
        key: function (i) {
          var p = scope();
          if (!p) {
            return null;
          }
          var mine = ownKeys(native, p);
          i = Number(i) || 0;
          return i >= 0 && i < mine.length ? mine[i] : null;
        },
        clear: function () {
          var p = scope();
          if (p) {
            ownKeys(native, p).forEach(function (k) {
              native.removeItem(p + k);
            });
          }
        },
      };

      var view;
      try {
        view = new Proxy(native, {
          get: function (t, prop) {
            // Symbols (Symbol.toStringTag among them) belong to the real object.
            if (typeof prop !== "string") {
              return Reflect.get(t, prop, t);
            }
            if (Object.prototype.hasOwnProperty.call(methods, prop)) {
              return methods[prop];
            }
            if (prop === "length") {
              var n = scope();
              return n ? ownKeys(t, n).length : 0;
            }
            var p = scope();
            if (!p) {
              return undefined;
            }
            var v = t.getItem(p + prop);
            return v === null ? undefined : v;
          },
          set: function (t, prop, value) {
            if (typeof prop !== "string") {
              return Reflect.set(t, prop, value);
            }
            var p = scope();
            if (p) {
              t.setItem(p + prop, String(value));
            }
            return true;
          },
          has: function (t, prop) {
            if (typeof prop !== "string") {
              return Reflect.has(t, prop);
            }
            if (prop === "length" || Object.prototype.hasOwnProperty.call(methods, prop)) {
              return true;
            }
            var p = scope();
            return !!p && t.getItem(p + prop) !== null;
          },
          deleteProperty: function (t, prop) {
            if (typeof prop !== "string") {
              return Reflect.deleteProperty(t, prop);
            }
            var p = scope();
            if (p) {
              t.removeItem(p + prop);
            }
            return true;
          },
          ownKeys: function (t) {
            var p = scope();
            return p ? ownKeys(t, p) : [];
          },
          getOwnPropertyDescriptor: function (t, prop) {
            if (typeof prop !== "string") {
              return Reflect.getOwnPropertyDescriptor(t, prop);
            }
            var p = scope();
            if (!p) {
              return undefined;
            }
            var v = t.getItem(p + prop);
            if (v === null) {
              return undefined;
            }
            // configurable must stay true: a Proxy may not report a
            // non-configurable property the target does not really have.
            return { value: v, writable: true, enumerable: true, configurable: true };
          },
        });
      } catch (e) {
        return; // no Proxy support: leave storage working rather than break it
      }

      try {
        Object.defineProperty(self, name, {
          configurable: true,
          enumerable: true,
          get: function () {
            return view;
          },
        });
      } catch (e) {
        /* a browser that will not let us redefine it keeps the native behaviour */
      }
    }

    function ownKeys(native, prefix) {
      var out = [];
      for (var i = 0; i < native.length; i++) {
        var k = native.key(i);
        if (k !== null && k.indexOf(prefix) === 0) {
          out.push(k.slice(prefix.length));
        }
      }
      return out;
    }

    wrapStorage("localStorage");
    wrapStorage("sessionStorage");

    // IndexedDB and CacheStorage need only their names namespaced; the contents
    // are then unreachable from another site's database.
    if (self.indexedDB && self.indexedDB.open) {
      var openDB = self.indexedDB.open.bind(self.indexedDB);
      var deleteDB = self.indexedDB.deleteDatabase.bind(self.indexedDB);
      self.indexedDB.open = function (name, version) {
        var p = scope();
        return version === undefined
          ? openDB(p ? p + name : name)
          : openDB(p ? p + name : name, version);
      };
      self.indexedDB.deleteDatabase = function (name) {
        var p = scope();
        return deleteDB(p ? p + name : name);
      };
    }

    if (self.caches && self.caches.open) {
      ["open", "has", "delete"].forEach(function (fn) {
        var nativeFn = self.caches[fn].bind(self.caches);
        self.caches[fn] = function (name) {
          var p = scope();
          return nativeFn(p ? p + name : name);
        };
      });
    }
  })();

  // --- register the service worker (safety net) ---
  // On the very first visit the worker is not controlling this document yet, so
  // early requests the in-page hooks cannot catch — dynamic import(), Worker(),
  // module preloads, anything racing before activation — escape the proxy. The
  // page then renders half-broken: subresources 404 and JS-drawn overlays (age
  // gates, consent modals) never appear. Once the worker takes control, reload
  // ONCE so the whole document renders under interception. A per-tab session
  // flag makes the reload fire at most once, so there is no reload loop.
  if (self.navigator && self.navigator.serviceWorker) {
    try {
      var swc = self.navigator.serviceWorker;
      swc.register("/__bumshi__/sw.js", { scope: "/" });
      if (!swc.controller) {
        var RELOAD_FLAG = "__bumshi_sw_reloaded__";
        swc.addEventListener("controllerchange", function () {
          try {
            if (self.sessionStorage && self.sessionStorage.getItem(RELOAD_FLAG)) {
              return;
            }
            if (self.sessionStorage) {
              self.sessionStorage.setItem(RELOAD_FLAG, "1");
            }
          } catch (e) {
            /* ignore */
          }
          self.location.reload();
        });
      }
    } catch (e) {
      /* ignore */
    }
  }
})(typeof self !== "undefined" ? self : window);
