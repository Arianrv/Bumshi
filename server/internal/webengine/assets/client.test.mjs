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

const TARGET = "https://www.youtube.com/watch?v=abc";

/**
 * Boots codec.js + rewriter.js + client.js on a page proxying TARGET.
 * Returns the context plus direct handles on the backing Storage objects, so a
 * test can see what was really persisted rather than what the shim reports.
 */
function boot() {
  const ctx = { URL, TextEncoder, TextDecoder, btoa, atob, console, Proxy, Reflect, Object, JSON, Number, String };
  ctx.self = ctx;
  ctx.window = ctx;

  const enc = Buffer.from(TARGET, "utf8").toString("base64url");
  ctx.location = {
    origin: "https://vps.example",
    pathname: "/p/" + enc,
    href: "https://vps.example/p/" + enc,
    reload() {},
  };
  ctx.document = { addEventListener() {}, documentElement: null, cookie: "" };
  ctx.navigator = { userAgent: "node" };
  ctx.addEventListener = () => {};

  const backing = { local: new Storage(), session: new Storage() };
  ctx.localStorage = backing.local;
  ctx.sessionStorage = backing.session;

  vm.createContext(ctx);
  for (const f of ["codec.js", "rewriter.js", "client.js"]) {
    vm.runInContext(fs.readFileSync(path.join(dir, f), "utf8"), ctx, { filename: f });
  }
  return { ctx, backing };
}

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
