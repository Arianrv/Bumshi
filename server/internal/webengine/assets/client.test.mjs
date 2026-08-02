// client.test.mjs — exercises the in-page hooks in client.js against a minimal
// browser-shaped context.
//
// The web-storage shim is the reason this file exists. It replaced localStorage
// and sessionStorage with a hand-written object carrying getItem/setItem, which
// looks complete and is not: most real code reaches keys as properties, and
// every one of those reads and writes silently bypassed the shim. The bug shows
// up only through property access and enumeration, so those are what these tests
// assert.
import { test } from "node:test";
import assert from "node:assert/strict";
import vm from "node:vm";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dir = path.dirname(fileURLToPath(import.meta.url));

/** A stand-in for a real Storage: a class, so instanceof and toStringTag work. */
class Storage {
  #m = new Map();
  getItem(k) {
    return this.#m.has(String(k)) ? this.#m.get(String(k)) : null;
  }
  setItem(k, v) {
    this.#m.set(String(k), String(v));
  }
  removeItem(k) {
    this.#m.delete(String(k));
  }
  key(i) {
    return [...this.#m.keys()][i] ?? null;
  }
  get length() {
    return this.#m.size;
  }
  get [Symbol.toStringTag]() {
    return "Storage";
  }
  /** Test-only view of what was really persisted, prefixes and all. */
  raw() {
    return Object.fromEntries(this.#m);
  }
}

/**
 * The browser's real cookie jar for the proxy origin: one flat namespace shared
 * by every proxied site, which is the whole reason names carry a scope.
 */
class Document {
  jar = new Map();
  addEventListener() {}
  documentElement = null;
  get cookie() {
    return [...this.jar].map(([k, v]) => k + "=" + v).join("; ");
  }
  set cookie(v) {
    const s = String(v);
    const eq = s.indexOf("=");
    if (eq < 0) return;
    this.jar.set(s.slice(0, eq).trim(), s.slice(eq + 1).split(";")[0]);
  }
}

const TARGET = "https://www.youtube.com/watch?v=abc";

/**
 * Boots codec.js + rewriter.js + client.js on a page proxying `target`.
 * Returns the context plus direct handles on the backing Storage and Document,
 * so a test can see what was really persisted rather than what the shims report.
 */
function boot(target = TARGET) {
  const ctx = { URL, TextEncoder, TextDecoder, btoa, atob, console, Proxy, Reflect, Object, JSON, Number, String, RegExp };
  ctx.self = ctx;
  ctx.window = ctx;

  const enc = Buffer.from(target, "utf8").toString("base64url");
  ctx.location = {
    origin: "https://vps.example",
    pathname: "/p/" + enc,
    href: "https://vps.example/p/" + enc,
    reload() {},
  };
  ctx.Document = Document;
  ctx.navigator = { userAgent: "node" };
  ctx.addEventListener = () => {};

  // A History that records what the page asked for, so a test can assert the
  // address the browser would actually end up showing.
  ctx.historyCalls = [];
  ctx.history = {
    pushState(state, title, url) {
      ctx.historyCalls.push(["pushState", url]);
    },
    replaceState(state, title, url) {
      ctx.historyCalls.push(["replaceState", url]);
    },
  };
  ctx.openCalls = [];
  ctx.open = (url) => {
    ctx.openCalls.push(url);
  };

  const backing = { local: new Storage(), session: new Storage(), doc: new Document() };
  ctx.document = backing.doc;
  ctx.localStorage = backing.local;
  ctx.sessionStorage = backing.session;

  vm.createContext(ctx);
  for (const f of ["codec.js", "rewriter.js", "client.js"]) {
    vm.runInContext(fs.readFileSync(path.join(dir, f), "utf8"), ctx, { filename: f });
  }
  return { ctx, backing };
}

// FNV-1a of "google.com", shared with cookies_test.go and RoutingTest.kt.
const GOOGLE_D = "b_e1a2c1ae38dcdf45d_";

// Shared with server/internal/proxy/cookies_test.go and RoutingTest.kt: the
// storage scope reuses the cookie-scope hash, so www.youtube.com must produce
// this exact prefix in all four places.
const YT_PREFIX = "b_9fbab5b679956134h_";

test("storage: property writes reach the backing store under a scoped key", () => {
  const { ctx, backing } = boot();
  ctx.localStorage.theme = "dark";

  // The point of the shim is that the value lands in real storage, namespaced.
  // The facade this replaced kept it on itself, so it vanished on reload.
  assert.deepEqual(backing.local.raw(), { [YT_PREFIX + "theme"]: "dark" });
  assert.equal(ctx.localStorage.theme, "dark");
  assert.equal(ctx.localStorage.getItem("theme"), "dark");
});

test("storage: enumeration reports stored keys, never the shim's methods", () => {
  const { ctx } = boot();
  const ls = ctx.localStorage;
  ls.setItem("session", "abc");
  ls.theme = "dark";

  const keys = Object.keys(ls).sort();
  assert.deepEqual(Array.from(keys), ["session", "theme"]);
  for (const bad of ["getItem", "setItem", "removeItem", "clear", "key"]) {
    assert.ok(!keys.includes(bad), `${bad} leaked into Object.keys`);
  }

  const seen = [];
  for (const k in ls) seen.push(k);
  assert.deepEqual(Array.from(seen).sort(), ["session", "theme"]);

  assert.deepEqual(JSON.parse(JSON.stringify(ls)), { session: "abc", theme: "dark" });
});

test("storage: in, delete and length behave like Storage", () => {
  const { ctx } = boot();
  const ls = ctx.localStorage;
  ls.a = "1";
  ls.b = "2";

  assert.ok("a" in ls);
  assert.ok(!("missing" in ls));
  assert.equal(ls.length, 2);
  assert.equal(ls.missing, undefined);

  delete ls.a;
  assert.ok(!("a" in ls));
  assert.equal(ls.length, 1);

  ls.clear();
  assert.equal(ls.length, 0);
});

test("storage: the site sees a bare key, the store holds a scoped one", () => {
  const { ctx, backing } = boot();
  ctx.localStorage.token = "secret";

  // Two sites sharing the proxy origin must not collide on "token", but neither
  // may see the prefix: it is an implementation detail of the shared jar.
  assert.deepEqual(Array.from(Object.keys(ctx.localStorage)), ["token"]);
  assert.equal(ctx.localStorage.getItem("token"), "secret");
  assert.deepEqual(Object.keys(backing.local.raw()), [YT_PREFIX + "token"]);
});

test("storage: still identifies as Storage", () => {
  const { ctx } = boot();
  // A script inspecting its environment must not find an object that fails the
  // checks every real browser passes.
  assert.equal(Object.prototype.toString.call(ctx.localStorage), "[object Storage]");
  assert.equal(typeof ctx.localStorage.getItem, "function");
  assert.equal(typeof ctx.localStorage.setItem, "function");
});

test("storage: localStorage and sessionStorage stay independent", () => {
  const { ctx } = boot();
  ctx.localStorage.k = "local";
  ctx.sessionStorage.k = "session";
  assert.equal(ctx.localStorage.k, "local");
  assert.equal(ctx.sessionStorage.k, "session");
});

// --- document.cookie scoping ---
//
// The server packs an upstream Set-Cookie under the scope its Domain= implies.
// A script setting the same cookie must land on that same name. When this shim
// dropped Domain= and always wrote host-only, the jar held two copies of one
// cookie and unpackCookies matched both, so the upstream request carried
// "Cookie: NID=<server>; NID=<script>".

test("cookie: a script's Domain= is scoped the way the server scopes Set-Cookie", () => {
  const { ctx, backing } = boot("https://www.google.com/search?q=x");
  ctx.document.cookie = "NID=JS_VALUE; domain=.google.com; path=/; Secure";

  assert.deepEqual(
    Object.keys(Object.fromEntries(backing.doc.jar)),
    [GOOGLE_D + "NID"],
    "a domain cookie must be stored under the domain scope, not host-only",
  );
});

test("cookie: server and script writes collapse onto one name", () => {
  const { ctx, backing } = boot("https://www.google.com/search?q=x");

  // What packSetCookie would have stored for: Set-Cookie: NID=A; Domain=.google.com
  backing.doc.jar.set(GOOGLE_D + "NID", "SERVER_VALUE");
  // What the page's own script stores for the same cookie.
  ctx.document.cookie = "NID=JS_VALUE; domain=.google.com";

  assert.equal(backing.doc.jar.size, 1, "one logical cookie must occupy one slot");
  assert.equal(backing.doc.jar.get(GOOGLE_D + "NID"), "JS_VALUE");

  // And the page reads back exactly one NID.
  const seen = ctx.document.cookie.split("; ").filter((c) => c.startsWith("NID="));
  assert.deepEqual(Array.from(seen), ["NID=JS_VALUE"]);
});

test("cookie: a host-only cookie stays host-only", () => {
  const { ctx, backing } = boot("https://www.google.com/search?q=x");
  ctx.document.cookie = "localpref=1";
  const [name] = [...backing.doc.jar.keys()];
  assert.ok(name.endsWith("h_localpref"), `expected host scope, got ${name}`);
});

test("cookie: a Domain the host is not inside is refused", () => {
  const { ctx, backing } = boot("https://www.google.com/search?q=x");
  ctx.document.cookie = "evil=1; domain=.example.com";
  const [name] = [...backing.doc.jar.keys()];
  assert.ok(name.endsWith("h_evil"), `must fall back to host scope, got ${name}`);
  assert.ok(!name.startsWith("b_" + "e1a2c1ae38dcdf45"), "must not borrow another scope");
});

test("cookie: a page sees only its own cookies, under real names", () => {
  const { ctx, backing } = boot("https://www.google.com/search?q=x");
  backing.doc.jar.set(GOOGLE_D + "NID", "mine");
  backing.doc.jar.set("b_ffffffffffffffffd_SESSION", "someone else's");
  assert.equal(ctx.document.cookie, "NID=mine");
});

// --- navigation hooks ---
//
// pushState is the escape nothing downstream can catch: it makes no network
// request, so the service worker and the host app never see it. If the address
// leaves "/p/", a reload 404s and realBase() stops decoding — every relative
// URL on the page then resolves against the proxy instead of the site.

/** The real URL a proxy path points at, or null. */
function decoded(ctx, proxyUrl) {
  const p = new URL(proxyUrl, "https://vps.example").pathname;
  return ctx.__bumshi.decodeProxied(p);
}

test("history: pushState keeps the address inside the proxy scheme", () => {
  const { ctx } = boot("https://www.youtube.com/watch?v=abc");
  ctx.history.pushState({}, "", "/watch?v=next");

  const [, url] = ctx.historyCalls[0];
  assert.equal(decoded(ctx, url), "https://www.youtube.com/watch?v=next");
});

test("history: replaceState resolves relative paths against the real site", () => {
  const { ctx } = boot("https://web.telegram.org/k/");
  ctx.history.replaceState(null, "", "settings");

  const [, url] = ctx.historyCalls[0];
  assert.equal(decoded(ctx, url), "https://web.telegram.org/k/settings");
});

test("history: an absolute cross-origin URL is proxied too", () => {
  const { ctx } = boot("https://www.google.com/search?q=x");
  ctx.history.pushState(null, "", "https://mail.google.com/inbox");

  const [, url] = ctx.historyCalls[0];
  assert.equal(decoded(ctx, url), "https://mail.google.com/inbox");
});

test("history: an omitted URL is left omitted, not invented", () => {
  const { ctx } = boot();
  ctx.history.pushState({ a: 1 }, "");
  assert.deepEqual(ctx.historyCalls[0], ["pushState", undefined]);
});

test("history: an already-proxied URL is not wrapped twice", () => {
  const { ctx } = boot("https://www.youtube.com/watch?v=abc");
  const once = "/p/" + ctx.__bumshi.encode("https://www.youtube.com/feed");
  ctx.history.pushState(null, "", once);

  const [, url] = ctx.historyCalls[0];
  assert.equal(decoded(ctx, url), "https://www.youtube.com/feed");
});

test("window.open is routed through the proxy", () => {
  const { ctx } = boot("https://www.google.com/search?q=x");
  ctx.open("https://example.com/popup");
  assert.equal(decoded(ctx, ctx.openCalls[0]), "https://example.com/popup");
});

test("a page cannot register its own service worker over ours", async () => {
  const registered = [];
  const { ctx } = boot();
  ctx.navigator.serviceWorker = {
    controller: {},
    register: (script, opts) => {
      registered.push(script);
      return Promise.resolve({});
    },
    addEventListener() {},
  };
  // Re-run client.js so the hook installs over this stand-in.
  vm.runInContext(fs.readFileSync(path.join(dir, "client.js"), "utf8"), ctx, { filename: "client.js" });

  await assert.rejects(() => ctx.navigator.serviceWorker.register("/sw.js", { scope: "/" }));
  assert.ok(
    !registered.includes("/sw.js"),
    "the page's worker must never reach the real register(): it would claim scope / and evict ours",
  );
  assert.ok(registered.includes("/__bumshi__/sw.js"), "our own worker must still register");
});
