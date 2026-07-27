#!/usr/bin/env bash
# Bumshi uninstaller.
#   bash <(curl -Ls https://raw.githubusercontent.com/Arianrv/Bumshi/main/uninstall.sh)
#   bash <(wget -qO- https://raw.githubusercontent.com/Arianrv/Bumshi/main/uninstall.sh)
#
# Removes Bumshi cleanly. It only ever removes Bumshi's own Caddy block (other
# sites are preserved) and it ASKS before deleting your data or removing shared
# software such as Caddy.
set -euo pipefail

BIN_DIR="/usr/local/bin"
ETC_DIR="/etc/bumshi"
STATE_DIR="/var/lib/bumshi"
UNIT="/etc/systemd/system/bumshi.service"
SERVICE_USER="bumshi"
CADDYFILE="/etc/caddy/Caddyfile"
MARK_BEGIN="# BEGIN bumshi managed block - do not edit"
MARK_END="# END bumshi managed block"

if [ -t 1 ]; then
  c_reset='\033[0m'; c_bold='\033[1m'; c_dim='\033[2m'
  c_teal='\033[36m'; c_green='\033[32m'; c_red='\033[31m'; c_yellow='\033[33m'
else
  c_reset=''; c_bold=''; c_dim=''; c_teal=''; c_green=''; c_red=''; c_yellow=''
fi
info() { echo -e "${c_teal}==>${c_reset} $*"; }
ok()   { echo -e "  ${c_green}✓${c_reset} $*"; }
warn() { echo -e "${c_yellow}!${c_reset} $*" >&2; }
section() {
  echo
  echo -e "${c_teal}${c_bold}$1${c_reset}"
  [ -n "${2:-}" ] && echo -e "${c_dim}$2${c_reset}"
  echo
}
have() { command -v "$1" >/dev/null 2>&1; }
interactive() { [ -t 0 ] && [ -e /dev/tty ]; }
[ "$(id -u)" -eq 0 ] || { echo "please run as root (try: sudo bash ...)" >&2; exit 1; }

confirm() {
  local q="$1" def="${2:-n}" ans hint
  if ! interactive; then [ "$def" = "y" ]; return; fi
  [ "$def" = "y" ] && hint="Y/n" || hint="y/N"
  read -rp "$(echo -e "${c_bold}${q}${c_reset} [${hint}]: ")" ans </dev/tty || true
  ans="${ans:-$def}"
  [[ "$ans" =~ ^[Yy]$ ]]
}

# spin "message" cmd... — show a live spinner during a slow step; ✓/✗ + log tail.
spin() {
  local msg="$1"; shift
  local log rc pid i=0 frames='|/-\'
  log="$(mktemp)"
  if ! [ -t 1 ]; then "$@" </dev/null >"$log" 2>&1; rc=$?; rm -f "$log"; return "$rc"; fi
  "$@" </dev/null >"$log" 2>&1 &
  pid=$!
  printf '  %s  ' "$msg"
  while kill -0 "$pid" 2>/dev/null; do printf '\b%s' "${frames:i++%4:1}"; sleep 0.1; done
  wait "$pid"; rc=$?
  if [ "$rc" -eq 0 ]; then printf '\b%b✓%b\n' "$c_green" "$c_reset"
  else printf '\b%b✗%b\n' "$c_red" "$c_reset"; echo -e "${c_dim}$(tail -n 4 "$log")${c_reset}" >&2; fi
  rm -f "$log"
  return "$rc"
}

section "Bumshi uninstaller" "Removes Bumshi. Other Caddy sites and shared software are left alone unless you choose otherwise."

if interactive && ! confirm "Remove Bumshi from this server?" n; then
  info "aborted — nothing was changed."
  exit 0
fi

# 1) Service
if [ -f "$UNIT" ] || systemctl status bumshi >/dev/null 2>&1; then
  systemctl disable --now bumshi >/dev/null 2>&1 || true
  rm -f "$UNIT"
  systemctl daemon-reload 2>/dev/null || true
  ok "stopped and removed the bumshi service"
fi

# 2) Caddy site block — remove ONLY Bumshi's marked block; keep every other site.
if [ -f "$CADDYFILE" ] && grep -qF "$MARK_BEGIN" "$CADDYFILE"; then
  bak="${CADDYFILE}.bumshi-bak.$(date +%Y%m%d%H%M%S)"
  cp "$CADDYFILE" "$bak"
  sed -i "\\|^${MARK_BEGIN}\$|,\\|^${MARK_END}\$|d" "$CADDYFILE"
  awk 'NF{last=NR} {line[NR]=$0} END{for (i = 1; i <= last; i++) print line[i]}' "$CADDYFILE" >"${CADDYFILE}.tmp" &&
    mv "${CADDYFILE}.tmp" "$CADDYFILE"
  ok "removed the Bumshi block from Caddy (backup: ${bak}; your other sites are untouched)"
  if have caddy; then
    if caddy validate --config "$CADDYFILE" >/dev/null 2>&1; then
      systemctl reload caddy 2>/dev/null || systemctl restart caddy 2>/dev/null || true
      ok "reloaded Caddy"
    else
      warn "Caddy did not validate after removal — check ${CADDYFILE} (backup at ${bak})"
    fi
  fi
fi

# 3) Binaries + management command
rm -f "${BIN_DIR}/bumshid" "${BIN_DIR}/bumshi"
ok "removed binaries and the management command"

# 4) Service user
if id -u "$SERVICE_USER" >/dev/null 2>&1; then
  userdel "$SERVICE_USER" 2>/dev/null || true
  ok "removed system user '${SERVICE_USER}'"
fi

# 5) Data — ask before deleting (this is your access-user roster and config).
if [ -d "$STATE_DIR" ]; then
  if confirm "Delete saved access users at ${STATE_DIR}? (removes your user roster)" n; then
    rm -rf "$STATE_DIR"; ok "removed ${STATE_DIR}"
  else
    info "kept ${STATE_DIR} (your users)"
  fi
fi
if [ -d "$ETC_DIR" ]; then
  if confirm "Delete configuration at ${ETC_DIR}?" n; then
    rm -rf "$ETC_DIR"; ok "removed ${ETC_DIR}"
  else
    info "kept ${ETC_DIR}"
  fi
fi

# 6) Shared software — never remove without explicit consent.
if have caddy; then
  section "Shared software" "Caddy may be serving OTHER sites on this box. Only remove it if nothing else uses it."
  if confirm "Also uninstall Caddy entirely?" n; then
    if have apt-get; then
      spin "Removing Caddy" apt-get remove -y caddy && ok "removed Caddy" || warn "could not remove Caddy automatically"
    else
      warn "not an apt system — remove Caddy with your package manager"
    fi
  else
    info "kept Caddy (still serving your other sites)"
  fi
fi

section "Done" "Bumshi has been removed."
