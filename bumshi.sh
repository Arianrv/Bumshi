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
SERVICE_USER="bumshi"

c_reset="\033[0m"; c_teal="\033[36m"; c_red="\033[31m"; c_green="\033[32m"; c_dim="\033[2m"

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

# reset_password — set a new admin panel password end to end: read it (hidden,
# with confirmation), hash it with the binary, write the hash into the env file
# (adding the key if missing), then restart so it takes effect. Because you are
# root on this box, this is the "forgot password" recovery path — no old
# password needed. Non-interactive: reads one password line from stdin.
reset_password() {
  need_systemd
  [ -x "$BINARY" ]   || { echo "binary not found at $BINARY" >&2; exit 1; }
  [ -f "$ENV_FILE" ] || { echo "config not found at $ENV_FILE" >&2; exit 1; }

  local p1 p2 hash
  if [ -t 0 ]; then
    read -rsp "New admin password: " p1 </dev/tty; echo
    read -rsp "Confirm password:   " p2 </dev/tty; echo
    [ -n "$p1" ]      || { echo "password cannot be empty" >&2; return 1; }
    [ "$p1" = "$p2" ] || { echo "passwords do not match" >&2; return 1; }
  else
    IFS= read -r p1 || true
    [ -n "$p1" ] || { echo "password required on stdin" >&2; return 1; }
  fi

  hash="$(printf '%s\n' "$p1" | "$BINARY" hash-password 2>/dev/null)" \
    || { echo "failed to hash password" >&2; return 1; }
  [ -n "$hash" ] || { echo "empty hash from binary" >&2; return 1; }

  # '#' is a safe sed delimiter here: a bcrypt hash only contains [./A-Za-z0-9$].
  if grep -q '^BUMSHI_ADMIN_PASSWORD_HASH=' "$ENV_FILE"; then
    sed -i "s#^BUMSHI_ADMIN_PASSWORD_HASH=.*#BUMSHI_ADMIN_PASSWORD_HASH=${hash}#" "$ENV_FILE"
  else
    echo "BUMSHI_ADMIN_PASSWORD_HASH=${hash}" >> "$ENV_FILE"
  fi

  # Keep the installer's ownership/permissions (sed -i may reset them).
  chown "$SERVICE_USER:$SERVICE_USER" "$ENV_FILE" 2>/dev/null || true
  chmod 640 "$ENV_FILE" 2>/dev/null || true

  restart
  echo -e "${c_green}✓${c_reset} admin password updated"
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
  hash-password               print an admin password hash (manual use)
  reset-password              set a new admin password (hash + restart)
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
    echo "  1) start          2) stop            3) restart"
    echo "  4) status         5) logs            6) follow logs"
    echo "  7) enable boot    8) disable boot    9) hash-password"
    echo " 10) reset password 11) edit config   12) rotate domain"
    echo " 13) update        14) version         0) exit"
    read -rp "> " choice
    case "$choice" in
      1) start;; 2) stop;; 3) restart;; 4) status;;
      5) logs;; 6) follow;; 7) enable;; 8) disable;;
      9) hash_password;; 10) reset_password;; 11) edit_env;; 12) rotate_domain;;
      13) update;; 14) version;;
      0) exit 0;; *) echo -e "${c_red}invalid choice${c_reset}";;
    esac
  done
}

cmd="${1:-menu}"
case "$cmd" in
  start) start;; stop) stop;; restart) restart;; status) status;;
  enable) enable;; disable) disable;;
  logs) logs "${2:-200}";; follow) follow;;
  hash-password) hash_password;; reset-password) reset_password;; edit) edit_env;; version) version;;
  rotate-domain) rotate_domain "${2:-}";; update) update;;
  menu) menu;;
  -h|--help|help) usage;;
  *) echo "unknown command: $cmd" >&2; usage; exit 2;;
esac
