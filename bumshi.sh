#!/usr/bin/env bash
# bumshi.sh — management menu for a systemd-based Bumshi install.
# Modeled on the 3x-ui management script, trimmed to what Bumshi needs.
set -euo pipefail

SERVICE="bumshi"
BINARY="/usr/local/bin/bumshid"
ENV_FILE="/etc/bumshi/bumshi.env"
CADDYFILE="/etc/caddy/Caddyfile"
REPO="${BUMSHI_REPO:-Arianrv/Bumshi}"
REF="${BUMSHI_REF:-main}"

c_reset="\033[0m"; c_teal="\033[36m"; c_red="\033[31m"; c_dim="\033[2m"

need_systemd() {
  if ! command -v systemctl >/dev/null 2>&1; then
    echo "systemctl not found. This menu manages a systemd install." >&2
    exit 1
  fi
}

svc()      { need_systemd; systemctl "$1" "$SERVICE"; }
start()    { svc start;   echo "started"; }
stop()     { svc stop;    echo "stopped"; }
restart()  { svc restart; echo "restarted"; }
status()   { need_systemd; systemctl --no-pager status "$SERVICE" || true; }
enable()   { svc enable;  echo "enabled at boot"; }
disable()  { svc disable; echo "disabled at boot"; }
logs()     { need_systemd; journalctl -u "$SERVICE" -n "${1:-200}" --no-pager; }
follow()   { need_systemd; journalctl -u "$SERVICE" -f; }

hash_password() {
  if [ ! -x "$BINARY" ]; then echo "binary not found at $BINARY" >&2; exit 1; fi
  "$BINARY" hash-password
  echo -e "${c_dim}Put the value above into ${ENV_FILE} as BUMSHI_ADMIN_PASSWORD_HASH, then: $0 restart${c_reset}"
}

edit_env() {
  need_systemd
  "${EDITOR:-vi}" "$ENV_FILE"
  echo "Restart to apply: $0 restart"
}

version() { [ -x "$BINARY" ] && "$BINARY" version || echo "binary not found at $BINARY"; }

rotate_domain() {
  need_systemd
  local d="${1:-}"
  [ -n "$d" ] || read -rp "New domain: " d
  [ -n "$d" ] || { echo "domain required" >&2; return 1; }
  if [ -f "$ENV_FILE" ]; then
    if grep -q '^BUMSHI_PUBLIC_URL=' "$ENV_FILE"; then
      sed -i "s#^BUMSHI_PUBLIC_URL=.*#BUMSHI_PUBLIC_URL=https://${d}#" "$ENV_FILE"
    else
      echo "BUMSHI_PUBLIC_URL=https://${d}" >> "$ENV_FILE"
    fi
  fi
  if [ -f "$CADDYFILE" ]; then
    cat > "$CADDYFILE" <<EOF
${d} {
	encode gzip
	header -Server
	reverse_proxy 127.0.0.1:8080
}
EOF
    systemctl reload caddy 2>/dev/null || systemctl restart caddy 2>/dev/null || true
  fi
  restart
  echo "rotated to ${d}"
}

update() {
  local url="https://raw.githubusercontent.com/${REPO}/${REF}/install.sh"
  echo "updating via installer ($url)"
  if command -v curl >/dev/null 2>&1; then bash <(curl -Ls "$url")
  else bash <(wget -qO- "$url"); fi
}

usage() {
  cat <<EOF
Bumshi management

Usage: $0 <command>

  start | stop | restart | status
  enable | disable            enable/disable start at boot
  logs [N] | follow           show last N log lines / follow live
  hash-password               generate an admin password hash
  edit                        edit ${ENV_FILE}
  rotate-domain [domain]      switch to a new domain (updates Caddy + env)
  update                      update to the latest release
  version
  menu                        interactive menu (default)
EOF
}

menu() {
  while true; do
    echo -e "\n${c_teal}بامشی · Bumshi${c_reset}"
    echo "  1) start         2) stop          3) restart"
    echo "  4) status        5) logs          6) follow logs"
    echo "  7) enable boot   8) disable boot  9) hash-password"
    echo " 10) edit config  11) rotate domain 12) update"
    echo " 13) version       0) exit"
    read -rp "> " choice
    case "$choice" in
      1) start;; 2) stop;; 3) restart;; 4) status;;
      5) logs;; 6) follow;; 7) enable;; 8) disable;;
      9) hash_password;; 10) edit_env;; 11) rotate_domain;; 12) update;;
      13) version;;
      0) exit 0;; *) echo -e "${c_red}invalid choice${c_reset}";;
    esac
  done
}

cmd="${1:-menu}"
case "$cmd" in
  start) start;; stop) stop;; restart) restart;; status) status;;
  enable) enable;; disable) disable;;
  logs) logs "${2:-200}";; follow) follow;;
  hash-password) hash_password;; edit) edit_env;; version) version;;
  rotate-domain) rotate_domain "${2:-}";; update) update;;
  menu) menu;;
  -h|--help|help) usage;;
  *) echo "unknown command: $cmd" >&2; usage; exit 2;;
esac
