#!/usr/bin/env bash
# Bumshi uninstaller. Run as root.
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "please run as root" >&2; exit 1; }

echo "==> stopping and removing the bumshi service"
systemctl disable --now bumshi 2>/dev/null || true
rm -f /etc/systemd/system/bumshi.service
systemctl daemon-reload 2>/dev/null || true

echo "==> removing binaries"
rm -f /usr/local/bin/bumshid /usr/local/bin/bumshi

if id -u bumshi >/dev/null 2>&1; then
  echo "==> removing system user 'bumshi'"
  userdel bumshi 2>/dev/null || true
fi

if [ -d /etc/bumshi ]; then
  read -rp "Remove configuration at /etc/bumshi? [y/N]: " ans || true
  if [[ "${ans:-n}" =~ ^[Yy]$ ]]; then
    rm -rf /etc/bumshi
    echo "==> removed /etc/bumshi"
  fi
fi

echo "Bumshi removed. Your Caddyfile (if any) was left untouched."
