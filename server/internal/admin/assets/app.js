// Bumshi admin panel — vanilla JS, no build step. Theme + i18n aware.
(function () {
  "use strict";
  var BASE = "__BASE__";
  var i18n = window.Bumshi.i18n;
  var theme = window.Bumshi.theme;
  var t = i18n.t;

  var currentView = "dashboard";

  function csrfToken() {
    var m = document.cookie.match(/(?:^|;\s*)bumshi_admin_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : "";
  }

  async function api(path, method, body) {
    var opts = { method: method || "GET", headers: {} };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.headers["X-CSRF-Token"] = csrfToken();
      opts.body = JSON.stringify(body);
    }
    var r = await fetch(BASE + "api/" + path, opts);
    if (r.status === 401) {
      location.href = BASE + "login";
      throw new Error("unauthorized");
    }
    var data = null;
    try { data = await r.json(); } catch (_) { /* ignore */ }
    if (!r.ok) throw new Error((data && data.error) || ("HTTP " + r.status));
    return data;
  }

  function el(tag, attrs, children) {
    var e = document.createElement(tag);
    if (attrs) {
      for (var k in attrs) {
        if (k === "class") e.className = attrs[k];
        else if (k === "text") e.textContent = attrs[k];
        else e.setAttribute(k, attrs[k]);
      }
    }
    (children || []).forEach(function (c) { e.appendChild(c); });
    return e;
  }

  function badge(on) {
    return el("span", { class: "badge " + (on ? "on" : "off"), text: on ? t("on") : t("off") });
  }

  // --- navigation ---
  function show(view) {
    currentView = view;
    ["dashboard", "access", "settings"].forEach(function (v) {
      document.getElementById("view-" + v).hidden = v !== view;
    });
    document.querySelectorAll("#nav a").forEach(function (a) {
      a.classList.toggle("active", a.dataset.view === view);
    });
    document.getElementById("title").textContent = t("nav_" + view);
    render(view);
  }

  function render(view) {
    if (view === "dashboard") loadDashboard();
    else if (view === "access") loadUsers();
    else if (view === "settings") loadSettings();
  }

  document.querySelectorAll("#nav a").forEach(function (a) {
    a.addEventListener("click", function () { show(a.dataset.view); });
  });

  document.getElementById("logout").addEventListener("click", async function () {
    try {
      await fetch(BASE + "logout", { method: "POST", headers: { "X-CSRF-Token": csrfToken() } });
    } catch (_) { /* ignore */ }
    location.href = BASE + "login";
  });

  // --- dashboard ---
  async function loadDashboard() {
    var s = await api("status");
    document.getElementById("build").textContent = "v" + s.version + " · " + s.go;
    var cards = document.getElementById("cards");
    cards.textContent = "";
    function card(label, node) {
      var c = el("div", { class: "card" }, [el("div", { class: "k", text: label })]);
      var v = el("div", { class: "v" });
      if (typeof node === "string") v.textContent = node; else v.appendChild(node);
      c.appendChild(v);
      return c;
    }
    cards.appendChild(card(t("card_proxy"), badge(s.settings.proxyEnabled)));
    cards.appendChild(card(t("card_access_log"), badge(s.settings.accessLog)));
    cards.appendChild(card(t("card_access_users"), String(s.accessUsers)));
    cards.appendChild(card(t("card_sessions"), String(s.sessions)));
    cards.appendChild(card(t("card_uptime"), fmtUptime(s.uptimeSeconds)));
    cards.appendChild(card(t("card_version"), s.version));
  }

  function fmtUptime(sec) {
    var d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
    if (d) return d + "d " + h + "h";
    if (h) return h + "h " + m + "m";
    return m + "m";
  }

  // --- access users ---
  function fmtDate(iso) {
    if (!iso) return "—";
    var d = new Date(iso);
    return isNaN(d.getTime()) ? "—" : d.toLocaleDateString();
  }

  async function loadUsers() {
    var users = await api("access-users");
    var tb = document.getElementById("users");
    tb.textContent = "";
    users.forEach(function (u) {
      var copyBtn = el("button", { class: "ghost", text: t("copy") });
      copyBtn.addEventListener("click", function () {
        if (navigator.clipboard) navigator.clipboard.writeText(u.link);
        copyBtn.textContent = t("copied");
        setTimeout(function () { copyBtn.textContent = t("copy"); }, 1200);
      });
      var del = el("button", { class: "link", text: t("delete") });
      del.addEventListener("click", async function () {
        await api("access-users/delete", "POST", { id: u.id });
        loadUsers();
      });
      var status = el("span", {
        class: "badge " + (u.expired ? "bad" : "ok"),
        text: u.expired ? t("status_expired") : t("status_active"),
      });
      tb.appendChild(el("tr", null, [
        el("td", { text: u.label }),
        el("td", { text: fmtDate(u.created) }),
        el("td", { text: u.expires ? fmtDate(u.expires) : t("expiry_never") }),
        el("td", null, [status]),
        el("td", null, [copyBtn]),
        el("td", null, [del]),
      ]));
    });
  }

  document.getElementById("add-user").addEventListener("click", async function () {
    var label = document.getElementById("new-label").value;
    var expiresDays = parseInt(document.getElementById("new-expiry").value, 10) || 0;
    await api("access-users", "POST", { label: label, expiresDays: expiresDays });
    document.getElementById("new-label").value = "";
    loadUsers();
  });

  // --- settings ---
  async function loadSettings() {
    var s = await api("settings");
    document.getElementById("s-proxy").checked = !!s.proxyEnabled;
    document.getElementById("s-access-log").checked = !!s.accessLog;
    document.getElementById("s-youtube").checked = !!(s.modules && s.modules.youtube);
    document.getElementById("s-telegram").checked = !!(s.modules && s.modules.telegram);
    document.getElementById("settings-msg").textContent = "";
  }

  document.getElementById("save-settings").addEventListener("click", async function () {
    var snap = {
      proxyEnabled: document.getElementById("s-proxy").checked,
      accessLog: document.getElementById("s-access-log").checked,
      modules: {
        youtube: document.getElementById("s-youtube").checked,
        telegram: document.getElementById("s-telegram").checked,
      },
    };
    var msg = document.getElementById("settings-msg");
    try {
      await api("settings", "POST", snap);
      msg.textContent = t("saved");
    } catch (e) {
      msg.textContent = t("error_prefix") + e.message;
    }
  });

  // --- theme + language controls ---
  function updateControls() {
    var btn = document.getElementById("theme-toggle");
    btn.textContent = theme.get() === "dark" ? t("theme_light") : t("theme_dark");
    document.getElementById("lang-select").value = i18n.currentLang();
  }

  (function initControls() {
    var sel = document.getElementById("lang-select");
    i18n.langs.forEach(function (l) {
      sel.appendChild(el("option", { value: l.code, text: l.name }));
    });
    sel.addEventListener("change", function () { i18n.setLang(sel.value); });
    document.getElementById("theme-toggle").addEventListener("click", function () { theme.toggle(); });
  })();

  window.addEventListener("bumshi:langchange", function () {
    document.getElementById("title").textContent = t("nav_" + currentView);
    updateControls();
    render(currentView);
  });
  window.addEventListener("bumshi:themechange", updateControls);

  // --- init ---
  i18n.apply(document);
  updateControls();
  show("dashboard");
})();
