// testutil.mjs — evaluate the shipped classic scripts (codec.js, rewriter.js)
// in an isolated context so tests exercise the exact files that are embedded and
// served, with no duplicated logic.
import vm from "node:vm";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dir = path.dirname(fileURLToPath(import.meta.url));

export function load(files) {
  const ctx = { URL, TextEncoder, TextDecoder, btoa, atob, console };
  ctx.self = ctx; // scripts use `self` as their global
  vm.createContext(ctx);
  for (const f of files) {
    const code = fs.readFileSync(path.join(dir, f), "utf8");
    vm.runInContext(code, ctx, { filename: f });
  }
  return ctx.__bumshi;
}
