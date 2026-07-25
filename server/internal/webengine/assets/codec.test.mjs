import { test } from "node:test";
import assert from "node:assert/strict";
import { load } from "./testutil.mjs";

test("base64url round trips arbitrary URLs", () => {
  const B = load(["codec.js"]);
  for (const s of [
    "https://example.com/watch?v=abc&x=1#frag",
    "https://xn--mgbh0fb.example/路径?q=%20",
    "http://a.b/c",
  ]) {
    assert.equal(B.decode(B.encode(s)), s);
  }
});

test("encode is url-safe and unpadded", () => {
  const B = load(["codec.js"]);
  const enc = B.encode("https://youtube.com/watch?v=xyz");
  assert.ok(!/[+/=]/.test(enc), `unexpected base64 chars in ${enc}`);
});
