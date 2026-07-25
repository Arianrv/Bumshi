// theme.js — dark/light theme with persistence. Attaches to window.Bumshi.theme.
(function (w) {
  "use strict";

  function get() {
    var t;
    try { t = localStorage.getItem("bumshi_theme"); } catch (e) { /* ignore */ }
    return t === "light" ? "light" : "dark";
  }

  function set(theme) {
    theme = theme === "light" ? "light" : "dark";
    document.documentElement.dataset.theme = theme;
    try { localStorage.setItem("bumshi_theme", theme); } catch (e) { /* ignore */ }
    w.dispatchEvent(new CustomEvent("bumshi:themechange", { detail: { theme: theme } }));
  }

  function toggle() { set(get() === "dark" ? "light" : "dark"); }

  w.Bumshi = w.Bumshi || {};
  w.Bumshi.theme = { get: get, set: set, toggle: toggle };
})(window);
