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
