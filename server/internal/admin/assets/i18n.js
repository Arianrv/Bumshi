// i18n.js — tiny translation layer for the admin panel.
// English (LTR) and Persian (RTL) are included; more languages can be added by
// extending DICT. Attaches to window.Bumshi.i18n. No build step.
(function (w) {
  "use strict";

  var DICT = {
    en: {
      login_subtitle: "Bumshi admin",
      username: "Username",
      password: "Password",
      sign_in: "Sign in",
      sign_in_failed: "Sign in failed",
      network_error: "Network error",
      nav_dashboard: "Dashboard",
      nav_access: "Access users",
      nav_settings: "Settings",
      logout: "Log out",
      card_proxy: "Proxy engine",
      card_access_log: "Access logging",
      card_access_users: "Access users",
      card_sessions: "Active sessions",
      card_uptime: "Uptime",
      card_version: "Version",
      on: "on",
      off: "off",
      access_label_ph: "Label (e.g. phone, laptop)",
      add_access_user: "Add access user",
      th_label: "Label",
      th_token: "Token",
      th_link: "Connection link",
      th_action: "",
      copy: "Copy",
      copied: "Copied",
      delete: "Delete",
      access_help:
        "Share the connection link (or its QR) with a person you trust. Access users live in memory and reset if the server restarts.",
      settings_proxy: "Web proxy engine (serve /p/)",
      settings_access_log: "Access logging",
      settings_access_log_note: "(dev only — keep off for public use)",
      settings_youtube: "YouTube module",
      settings_telegram: "Telegram module",
      coming_soon: "(coming soon)",
      save_settings: "Save settings",
      saved: "Saved.",
      error_prefix: "Error: ",
      theme_dark: "Dark",
      theme_light: "Light",
      language: "Language",
    },
    fa: {
      login_subtitle: "پنل مدیریت بامشی",
      username: "نام کاربری",
      password: "گذرواژه",
      sign_in: "ورود",
      sign_in_failed: "ورود ناموفق بود",
      network_error: "خطای شبکه",
      nav_dashboard: "داشبورد",
      nav_access: "کاربران دسترسی",
      nav_settings: "تنظیمات",
      logout: "خروج",
      card_proxy: "موتور پروکسی",
      card_access_log: "ثبت درخواست‌ها",
      card_access_users: "کاربران دسترسی",
      card_sessions: "نشست‌های فعال",
      card_uptime: "مدت فعالیت",
      card_version: "نسخه",
      on: "روشن",
      off: "خاموش",
      access_label_ph: "برچسب (مثلاً موبایل، لپ‌تاپ)",
      add_access_user: "افزودن کاربر",
      th_label: "برچسب",
      th_token: "توکن",
      th_link: "لینک اتصال",
      th_action: "",
      copy: "کپی",
      copied: "کپی شد",
      delete: "حذف",
      access_help:
        "لینک اتصال (یا کد QR آن) را فقط با فردی که به او اعتماد دارید به اشتراک بگذارید. کاربران دسترسی در حافظه نگه‌داری می‌شوند و با راه‌اندازی مجدد سرور پاک می‌شوند.",
      settings_proxy: "موتور پروکسی وب (ارائه‌ی ‎/p/‎)",
      settings_access_log: "ثبت درخواست‌ها",
      settings_access_log_note: "(فقط برای توسعه — برای استفاده‌ی عمومی خاموش بماند)",
      settings_youtube: "ماژول یوتیوب",
      settings_telegram: "ماژول تلگرام",
      coming_soon: "(به‌زودی)",
      save_settings: "ذخیره‌ی تنظیمات",
      saved: "ذخیره شد.",
      error_prefix: "خطا: ",
      theme_dark: "تیره",
      theme_light: "روشن",
      language: "زبان",
    },
  };

  var LANGS = [
    { code: "en", name: "English" },
    { code: "fa", name: "فارسی" },
  ];

  function currentLang() {
    var l;
    try { l = localStorage.getItem("bumshi_lang"); } catch (e) { /* ignore */ }
    return DICT[l] ? l : "en";
  }

  function t(key) {
    var l = currentLang();
    return (DICT[l] && DICT[l][key]) || DICT.en[key] || key;
  }

  // apply sets translated text on [data-i18n] / [data-i18n-ph] elements and
  // updates the document language and direction (RTL for Persian).
  function apply(root) {
    var scope = root || document;
    var lang = currentLang();
    var el = document.documentElement;
    el.lang = lang;
    el.dir = lang === "fa" ? "rtl" : "ltr";
    scope.querySelectorAll("[data-i18n]").forEach(function (n) {
      n.textContent = t(n.getAttribute("data-i18n"));
    });
    scope.querySelectorAll("[data-i18n-ph]").forEach(function (n) {
      n.setAttribute("placeholder", t(n.getAttribute("data-i18n-ph")));
    });
  }

  function setLang(lang) {
    if (!DICT[lang]) return;
    try { localStorage.setItem("bumshi_lang", lang); } catch (e) { /* ignore */ }
    apply(document);
    w.dispatchEvent(new CustomEvent("bumshi:langchange", { detail: { lang: lang } }));
  }

  w.Bumshi = w.Bumshi || {};
  w.Bumshi.i18n = { t: t, apply: apply, setLang: setLang, currentLang: currentLang, langs: LANGS };
})(window);
