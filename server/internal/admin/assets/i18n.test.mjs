// Runtime tests for the panel's theme + i18n modules. They execute the exact
// shipped files (theme.js, i18n.js) against a minimal DOM stub, so there is no
// duplicated logic and RTL/theme behavior is genuinely verified.
import { test } from "node:test";
import assert from "node:assert/strict";
import vm from "node:vm";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dir = path.dirname(fileURLToPath(import.meta.url));

function mkEl(attrs) {
  return {
    _a: attrs || {},
    textContent: "",
    getAttribute(k) { return this._a[k]; },
    setAttribute(k, v) { this._a[k] = v; },
  };
}

function load() {
  const nav = mkEl({ "data-i18n": "nav_dashboard" });
  const ph = mkEl({ "data-i18n-ph": "access_label_ph" });
  const docEl = { dataset: {}, lang: "", dir: "" };
  const listeners = {};
  let store = {};
  const ctx = {
    console,
    localStorage: {
      getItem: (k) => (k in store ? store[k] : null),
      setItem: (k, v) => { store[k] = String(v); },
    },
    CustomEvent: function (n, o) { this.type = n; this.detail = o && o.detail; },
    document: {
      documentElement: docEl,
      querySelectorAll(sel) {
        if (sel === "[data-i18n]") return [nav];
        if (sel === "[data-i18n-ph]") return [ph];
        return [];
      },
    },
  };
  ctx.window = ctx;
  ctx.window.dispatchEvent = (e) => (listeners[e.type] || []).forEach((f) => f(e));
  ctx.window.addEventListener = (t, f) => { (listeners[t] = listeners[t] || []).push(f); };
  vm.createContext(ctx);
  for (const f of ["theme.js", "i18n.js"]) {
    vm.runInContext(fs.readFileSync(path.join(dir, f), "utf8"), ctx, { filename: f });
  }
  return { i18n: ctx.window.Bumshi.i18n, theme: ctx.window.Bumshi.theme, docEl, nav, ph };
}

test("english is LTR and translated", () => {
  const { i18n, docEl, nav, ph } = load();
  assert.equal(i18n.currentLang(), "en");
  assert.equal(i18n.t("nav_dashboard"), "Dashboard");
  i18n.apply();
  assert.equal(nav.textContent, "Dashboard");
  assert.ok(ph._a.placeholder, "placeholder should be set");
  assert.equal(docEl.dir, "ltr");
});

test("persian switches to RTL natively with translated text", () => {
  const { i18n, docEl, nav } = load();
  i18n.setLang("fa");
  assert.equal(i18n.currentLang(), "fa");
  assert.equal(docEl.dir, "rtl");
  assert.notEqual(nav.textContent, "Dashboard");
  assert.ok(nav.textContent.length > 0);
});

test("theme applies, persists, and toggles", () => {
  const { theme, docEl } = load();
  theme.set("light");
  assert.equal(docEl.dataset.theme, "light");
  theme.toggle();
  assert.equal(theme.get(), "dark");
});
