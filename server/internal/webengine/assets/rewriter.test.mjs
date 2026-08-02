import { test } from "node:test";
import assert from "node:assert/strict";
import { load } from "./testutil.mjs";

const PROXY = "https://vps.example";

test("resolve relative references", () => {
  const B = load(["codec.js", "rewriter.js"]);
  const base = "https://example.com/dir/page.html";
  assert.equal(B.resolve(base, "img.png"), "/p/" + B.encode("https://example.com/dir/img.png"));
  assert.equal(B.resolve(base, "/root"), "/p/" + B.encode("https://example.com/root"));
  assert.equal(B.resolve(base, "https://cdn.net/a.js"), "/p/" + B.encode("https://cdn.net/a.js"));
});

test("resolve leaves non-navigational references untouched", () => {
  const B = load(["codec.js", "rewriter.js"]);
  for (const ref of ["#top", "data:image/png;base64,AAAA", "javascript:void(0)", "mailto:a@b.c"]) {
    assert.equal(B.resolve("https://e.com/", ref), ref);
  }
});

test("rewriteRequestURL maps a same-origin subresource back onto the target", () => {
  const B = load(["codec.js", "rewriter.js"]);
  const clientUrl = PROXY + "/p/" + B.encode("https://youtube.com/watch");
  const got = B.rewriteRequestURL(PROXY, clientUrl, PROXY + "/api/data?x=1");
  assert.equal(got, PROXY + "/p/" + B.encode("https://youtube.com/api/data?x=1"));
});

test("rewriteRequestURL proxies a cross-origin request", () => {
  const B = load(["codec.js", "rewriter.js"]);
  const clientUrl = PROXY + "/p/" + B.encode("https://youtube.com/watch");
  const got = B.rewriteRequestURL(PROXY, clientUrl, "https://cdn.other.com/a.js");
  assert.equal(got, PROXY + "/p/" + B.encode("https://cdn.other.com/a.js"));
});

test("rewriteRequestURL leaves already-proxied and engine URLs unchanged", () => {
  const B = load(["codec.js", "rewriter.js"]);
  const proxied = PROXY + "/p/" + B.encode("https://youtube.com/x");
  assert.equal(B.rewriteRequestURL(PROXY, proxied, proxied), proxied);
  const asset = PROXY + "/__bumshi__/client.js";
  assert.equal(B.rewriteRequestURL(PROXY, proxied, asset), asset);
});

// These vectors are duplicated in server/internal/proxy/cookies_test.go. The
// two implementations must agree byte for byte: if they ever drift, every
// cookie already in a user's jar becomes unreadable at once.
test("cookie prefixes match the Go implementation", () => {
  const B = load(["codec.js", "rewriter.js"]);
  assert.equal(B.cookiePrefix("google.com", false), "b_e1a2c1ae38dcdf45h_");
  assert.equal(B.cookiePrefix("google.com", true), "b_e1a2c1ae38dcdf45d_");
  assert.equal(B.cookiePrefix("www.youtube.com", true), "b_9fbab5b679956134d_");
  assert.equal(B.cookiePrefix("accounts.google.com", false), "b_3f7578df6dd846d1h_");
  // Case-insensitive, like a hostname.
  assert.equal(B.cookiePrefix("ExAmPlE.CoM", true), B.cookiePrefix("example.com", true));
});

test("scopePrefixes walks parents and stops at two labels", () => {
  const B = load(["codec.js", "rewriter.js"]);
  // Array.from: the scripts run in a vm context, so their arrays carry a
  // different Array.prototype and would fail a strict deep comparison.
  assert.deepEqual(Array.from(B.scopePrefixes("accounts.google.com")), [
    "b_3f7578df6dd846d1h_",
    "b_3f7578df6dd846d1d_",
    "b_e1a2c1ae38dcdf45d_",
  ]);
  assert.equal(B.scopePrefixes("a.b.example.com").length, 4);
  assert.equal(B.scopePrefixes("").length, 0);
});

test("decodeProxied requires the token to decode to an http(s) URL", () => {
  const B = load(["codec.js", "rewriter.js"]);
  assert.equal(B.decodeProxied("/p/" + B.encode("https://a.com/x")), "https://a.com/x");
  // A real site's own /p/ path: valid base64 characters, but not a URL.
  assert.equal(B.decodeProxied("/p/product/123"), null);
  assert.equal(B.decodeProxied("/p/"), null);
  assert.equal(B.decodeProxied("/other"), null);
  assert.equal(B.decodeProxied("/p/" + B.encode("ftp://a.com/x")), null);
});

test("a target site's own /p/ path is still proxied", () => {
  const B = load(["codec.js", "rewriter.js"]);
  const clientUrl = PROXY + "/p/" + B.encode("https://shop.example/home");
  assert.equal(B.isProxiedPath("/p/product/123"), false);
  assert.equal(B.isProxiedPath("/__bumshi__/sw.js"), true);
  const got = B.rewriteRequestURL(PROXY, clientUrl, PROXY + "/p/product/123");
  assert.equal(got, PROXY + "/p/" + B.encode("https://shop.example/p/product/123"));
});

// --- ES module recovery ---
//
// Because the target sits in the PATH, the browser's own module resolver
// destroys the token: "./lang.js" imported from "/p/<token of .../k/index.js>"
// becomes "/p/lang.js". No JavaScript hook can intercept a module specifier, so
// the service worker is the only place this can be repaired — and the tail
// after the prefix is exactly the specifier, already resolved against the
// importing file's directory.

const MOD = "https://web.telegram.org/k/index-COc_Pe9u.js";
const DOC = "https://web.telegram.org/k/";

test("module: a relative import is recovered against the importing file", () => {
  const B = load(["codec.js", "rewriter.js"]);
  // What the browser actually asks for after mangling "./lang-R0uxlNrr.js".
  const out = B.rewriteRequestURL(PROXY, PROXY + "/p/" + B.encode(MOD), PROXY + "/p/lang-R0uxlNrr.js", "script");
  assert.equal(out, PROXY + "/p/" + B.encode("https://web.telegram.org/k/lang-R0uxlNrr.js"));
});

test("module: a nested relative import keeps its directory", () => {
  const B = load(["codec.js", "rewriter.js"]);
  const out = B.rewriteRequestURL(PROXY, PROXY + "/p/" + B.encode(MOD), PROXY + "/p/chunks/deep.js", "script");
  assert.equal(out, PROXY + "/p/" + B.encode("https://web.telegram.org/k/chunks/deep.js"));
});

test("module: a worker URL built from import.meta.url is recovered", () => {
  const B = load(["codec.js", "rewriter.js"]);
  const out = B.rewriteRequestURL(PROXY, PROXY + "/p/" + B.encode(MOD), PROXY + "/p/webp.worker-BhH2.js", "worker");
  assert.equal(out, PROXY + "/p/" + B.encode("https://web.telegram.org/k/webp.worker-BhH2.js"));
});

test("module: recovery never touches a non-module request", () => {
  // A target site's own "/p/product/123" has the same shape as the wreckage and
  // must survive. It arrives as a document, an image or a plain fetch — never
  // as a script — which is what makes the two separable at all.
  const B = load(["codec.js", "rewriter.js"]);
  for (const dest of ["document", "image", "", "style", undefined]) {
    const out = B.rewriteRequestURL(PROXY, PROXY + "/p/" + B.encode(DOC), PROXY + "/p/product/123", dest);
    assert.equal(
      out,
      PROXY + "/p/" + B.encode("https://web.telegram.org/p/product/123"),
      `destination ${dest || "(empty)"} must not be treated as module wreckage`,
    );
  }
});

test("module: a still-valid token is left alone even on a script request", () => {
  const B = load(["codec.js", "rewriter.js"]);
  const good = PROXY + "/p/" + B.encode("https://web.telegram.org/k/real.js");
  assert.equal(B.rewriteRequestURL(PROXY, PROXY + "/p/" + B.encode(MOD), good, "script"), good);
});
