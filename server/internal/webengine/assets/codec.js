// codec.js — base64url encode/decode of the target URL, matching the Go
// scheme in server/internal/proxy/link (base64.RawURLEncoding of the UTF-8
// bytes of the absolute URL string, no padding).
//
// Loaded as a classic script in both the page (via <script>) and the service
// worker (via importScripts), and evaluated in Node for tests. It attaches its
// API to self.__bumshi so there is a single source of truth (no duplication).
(function (self) {
  "use strict";
  var B = (self.__bumshi = self.__bumshi || {});

  function encode(str) {
    var bytes = new TextEncoder().encode(String(str));
    var bin = "";
    for (var i = 0; i < bytes.length; i++) {
      bin += String.fromCharCode(bytes[i]);
    }
    return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }

  function decode(token) {
    var s = String(token).replace(/-/g, "+").replace(/_/g, "/");
    while (s.length % 4) {
      s += "=";
    }
    var bin = atob(s);
    var bytes = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) {
      bytes[i] = bin.charCodeAt(i);
    }
    return new TextDecoder().decode(bytes);
  }

  B.encode = encode;
  B.decode = decode;
})(typeof self !== "undefined" ? self : globalThis);
