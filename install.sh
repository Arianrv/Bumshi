#!/usr/bin/env bash
# Bumshi installer.
#
# Recommended (process substitution keeps prompts interactive):
#   bash <(curl -Ls https://raw.githubusercontent.com/Arianrv/Bumshi/main/install.sh)
#   bash <(wget -qO- https://raw.githubusercontent.com/Arianrv/Bumshi/main/install.sh)
#
# Non-interactive (CI/automation): set BUMSHI_* variables and pipe, e.g.
#   BUMSHI_DOMAIN=proxy.example.com BUMSHI_ADMIN_PASSWORD=... \
#   BUMSHI_TLS=letsencrypt BUMSHI_EMAIL=you@example.com curl -Ls URL | bash
#
# It installs the bumshid binary (checksum-verified), a hardened systemd unit,
# the management command, an env file, and optionally wires TLS via Caddy. It is
# idempotent: re-running updates the binary and never clobbers an existing Caddy
# config — a Bumshi site block is added between markers and refreshed in place.
set -euo pipefail

REPO="${BUMSHI_REPO:-Arianrv/Bumshi}"
REF="${BUMSHI_REF:-main}"           # branch/tag for helper files (unit, mgmt script)
VERSION="${BUMSHI_VERSION:-latest}" # release tag for the binary, or "latest"

BIN_DIR="/usr/local/bin"
BINARY="${BIN_DIR}/bumshid"
MGMT="${BIN_DIR}/bumshi"
ETC_DIR="/etc/bumshi"
ENV_FILE="${ETC_DIR}/bumshi.env"
UNIT="/etc/systemd/system/bumshi.service"
SERVICE_USER="bumshi"
CADDYFILE="/etc/caddy/Caddyfile"
MARK_BEGIN="# BEGIN bumshi managed block - do not edit"
MARK_END="# END bumshi managed block"

# ---- colors (disabled when stdout is not a TTY) ----
if [ -t 1 ]; then
  c_reset='\033[0m'; c_bold='\033[1m'; c_dim='\033[2m'
  c_teal='\033[36m'; c_green='\033[32m'; c_red='\033[31m'; c_yellow='\033[33m'
else
  c_reset=''; c_bold=''; c_dim=''; c_teal=''; c_green=''; c_red=''; c_yellow=''
fi

info() { echo -e "${c_teal}==>${c_reset} $*"; }
ok()   { echo -e "  ${c_green}✓${c_reset} $*"; }
warn() { echo -e "${c_yellow}!${c_reset} $*" >&2; }
err()  { echo -e "${c_red}✗ error:${c_reset} $*" >&2; }
die()  { err "$*"; exit 1; }
section() {
  echo
  echo -e "${c_teal}${c_bold}$1${c_reset}"
  [ -n "${2:-}" ] && echo -e "${c_dim}$2${c_reset}"
  echo
}

have() { command -v "$1" >/dev/null 2>&1; }
interactive() { [ -t 0 ] && [ -e /dev/tty ]; }
require_root() { [ "$(id -u)" -eq 0 ] || die "please run as root (try: sudo bash ...)"; }

raw_url() { echo "https://raw.githubusercontent.com/${REPO}/${REF}/$1"; }
rel_url() { echo "https://github.com/${REPO}/releases/download/$1/$2"; }

download() { # download URL OUT
  if have curl; then curl -fsSL "$1" -o "$2"
  elif have wget; then wget -qO "$2" "$1"
  else die "need curl or wget"; fi
}
fetch() { # fetch URL -> stdout
  if have curl; then curl -fsSL "$1"
  elif have wget; then wget -qO- "$1"
  else die "need curl or wget"; fi
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac
}

gen_password() { LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom 2>/dev/null | head -c 16 || true; }

# ---- input helpers ----

# ask VAR "Label" "one-line description" "default"
# Prints a bold label + dim description, then a clear "> " input line. Returns
# the default (or the seeded value) non-interactively.
ask() {
  local __var="$1" label="$2" desc="${3:-}" def="${4:-}" ans
  if ! interactive; then printf -v "$__var" '%s' "$def"; return; fi
  echo -e "${c_bold}${label}${c_reset}"
  [ -n "$desc" ] && echo -e "${c_dim}${desc}${c_reset}"
  if [ -n "$def" ]; then read -rp "$(echo -e "  ${c_teal}›${c_reset} [${def}]: ")" ans </dev/tty || true
  else read -rp "$(echo -e "  ${c_teal}›${c_reset} ")" ans </dev/tty || true; fi
  printf -v "$__var" '%s' "${ans:-$def}"
  echo
}

# ask_secret VAR "Label" "description"  (input hidden)
ask_secret() {
  local __var="$1" label="$2" desc="${3:-}" ans
  if ! interactive; then printf -v "$__var" '%s' ""; return; fi
  echo -e "${c_bold}${label}${c_reset}"
  [ -n "$desc" ] && echo -e "${c_dim}${desc}${c_reset}"
  read -rsp "$(echo -e "  ${c_teal}›${c_reset} ")" ans </dev/tty || true
  echo; echo
  printf -v "$__var" '%s' "$ans"
}

# confirm "question" default(y|n)
confirm() {
  local q="$1" def="${2:-n}" ans hint
  if ! interactive; then [ "$def" = "y" ]; return; fi
  [ "$def" = "y" ] && hint="Y/n" || hint="y/N"
  read -rp "$(echo -e "${c_bold}${q}${c_reset} [${hint}]: ")" ans </dev/tty || true
  ans="${ans:-$def}"
  [[ "$ans" =~ ^[Yy]$ ]]
}

# choose VAR "prompt" "opt1" "opt2" ...  -> stores the chosen option NUMBER (1-based)
choose() {
  local __var="$1"; shift
  local q="$1"; shift
  local opts=("$@") i ans
  if ! interactive; then printf -v "$__var" '%s' 1; return; fi
  echo -e "${c_bold}${q}${c_reset}"
  for i in "${!opts[@]}"; do echo -e "  ${c_teal}$((i + 1)))${c_reset} ${opts[$i]}"; done
  while :; do
    read -rp "$(echo -e "  ${c_teal}›${c_reset} [1]: ")" ans </dev/tty || true
    ans="${ans:-1}"
    [[ "$ans" =~ ^[0-9]+$ ]] && [ "$ans" -ge 1 ] && [ "$ans" -le "${#opts[@]}" ] && break
    warn "enter a number between 1 and ${#opts[@]}"
  done
  printf -v "$__var" '%s' "$ans"
  echo
}

normalize_domain() { echo "$1" | tr 'A-Z' 'a-z' | sed -E 's#^https?://##; s#/.*$##; s/[[:space:]]//g'; }
valid_domain() { [[ "$1" =~ ^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$ ]]; }
valid_email() { [[ "$1" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]; }
normalize_path() { # ensure leading + trailing slash, no spaces
  local p; p="$(echo "$1" | tr -d '[:space:]')"
  [ -n "$p" ] || p="admin"
  [[ "$p" == /* ]] || p="/$p"
  [[ "$p" == */ ]] || p="$p/"
  echo "$p"
}
port_in_use() { # port_in_use PORT
  if have ss; then ss -ltn "( sport = :$1 )" 2>/dev/null | grep -q ":$1 "
  elif have lsof; then lsof -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
  else return 1; fi
}

# ---- install steps ----

resolve_latest() {
  fetch "https://api.github.com/repos/${REPO}/releases/latest" |
    grep -m1 '"tag_name"' |
    sed -E 's/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/'
}

install_binary() { # install_binary TAG ARCH
  local tag="$1" arch="$2" asset="bumshid_linux_$2" tmp
  tmp="$(mktemp -d)"
  info "downloading ${asset} (${tag})"
  download "$(rel_url "$tag" "$asset")" "${tmp}/${asset}"
  download "$(rel_url "$tag" checksums.txt)" "${tmp}/checksums.txt"
  (cd "$tmp" && grep " ${asset}\$" checksums.txt | sha256sum -c - >/dev/null) ||
    { rm -rf "$tmp"; die "checksum verification failed"; }
  install -m 0755 "${tmp}/${asset}" "$BINARY"
  rm -rf "$tmp"
  ok "installed ${BINARY}"
}

ensure_user() {
  if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER" 2>/dev/null ||
      useradd --system --no-create-home --shell /bin/false "$SERVICE_USER"
    ok "created system user ${SERVICE_USER}"
  fi
}

install_unit() {
  download "$(raw_url deploy/systemd/bumshi.service)" "$UNIT" || die "could not download systemd unit"
  systemctl daemon-reload
  ok "installed systemd unit"
}

install_mgmt() {
  if download "$(raw_url bumshi.sh)" "$MGMT" 2>/dev/null; then
    chmod 0755 "$MGMT"
    ok "installed management command: bumshi"
  else
    warn "could not fetch management script (bumshi.sh)"
  fi
}

write_env() {
  local hash=""
  if [ -n "$admin_pass" ]; then
    hash="$(printf '%s\n' "$admin_pass" | "$BINARY" hash-password 2>/dev/null)" ||
      die "failed to hash admin password"
  fi
  mkdir -p "$ETC_DIR"
  (umask 077; cat >"$ENV_FILE" <<EOF
# Generated by install.sh — edit and then: bumshi restart
BUMSHI_ENV=production
BUMSHI_LISTEN_ADDR=127.0.0.1:8080
BUMSHI_METRICS_ADDR=127.0.0.1:9090
BUMSHI_ACCESS_LOG=false

BUMSHI_PROXY_ENABLED=${enable_proxy}
BUMSHI_PROXY_FORCE_IPV4=true
# Turn on ONLY after every client is updated to send its access token:
BUMSHI_PROXY_REQUIRE_TOKEN=false

BUMSHI_ADMIN_ENABLED=true
BUMSHI_ADMIN_PATH=${admin_path}
BUMSHI_ADMIN_USERNAME=${admin_user}
BUMSHI_ADMIN_PASSWORD_HASH=${hash}
BUMSHI_PUBLIC_URL=${public_url}
EOF
  )
  chown -R "$SERVICE_USER:$SERVICE_USER" "$ETC_DIR"
  chmod 640 "$ENV_FILE"
  ok "wrote ${ENV_FILE}"
}

install_caddy_pkg() {
  have apt-get || return 1
  info "installing Caddy"
  apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg >/dev/null 2>&1 || true
  curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key |
    gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt |
    tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
  apt-get update -y >/dev/null && apt-get install -y caddy >/dev/null
}

# emit_site_block — prints the marked Bumshi Caddy site block to stdout.
emit_site_block() {
  echo "$MARK_BEGIN"
  echo "${domain} {"
  echo "	encode zstd gzip"
  echo "	header -Server"
  if [ "$cert_mode" = "cf" ]; then echo "	tls ${cf_cert} ${cf_key}"; fi
  echo "	reverse_proxy 127.0.0.1:8080"
  echo "}"
  echo "$MARK_END"
}

# setup_tls — wires HTTPS for $domain without ever clobbering an existing config.
setup_tls() {
  [ -n "$domain" ] || { warn "no domain — skipping TLS. Point your own reverse proxy at 127.0.0.1:8080."; return; }

  # Interactive: pick a method. Non-interactive: honor BUMSHI_TLS (default skip).
  if interactive; then
    section "TLS" "How should ${domain} get its HTTPS certificate?"
    local m
    choose m "Certificate method" \
      "Cloudflare Origin Certificate  (recommended when the domain is proxied through Cloudflare)" \
      "Let's Encrypt  (automatic; requires the domain to point DIRECTLY at this server, no Cloudflare proxy)" \
      "I'll handle TLS myself  (skip — just run bumshid on 127.0.0.1:8080)"
    case "$m" in
      1) cert_mode="cf" ;;
      2) cert_mode="le" ;;
      3) cert_mode="skip" ;;
    esac
  fi
  if [ "$cert_mode" = "skip" ]; then
    behind_cf="n"
    warn "skipping TLS; reverse-proxy your own TLS to 127.0.0.1:8080"
    return
  fi

  if [ "$cert_mode" = "cf" ]; then
    behind_cf="y"
    local root; root="$(echo "$domain" | rev | cut -d. -f1,2 | rev)"
    echo -e "${c_dim}Create it in Cloudflare -> SSL/TLS -> Origin Server -> Create Certificate"
    echo -e "(hosts: ${root}, *.${root}), then save the certificate (PEM) and key on this box.${c_reset}"
    echo
    ask cf_cert "Origin certificate path" "the PEM certificate body from Cloudflare" "${cf_cert:-/etc/caddy/${domain}.pem}"
    ask cf_key  "Origin key path" "the PEM private key from Cloudflare" "${cf_key:-/etc/caddy/${domain}.key}"
    if [ ! -s "$cf_cert" ] || [ ! -s "$cf_key" ]; then
      warn "cert/key not found at those paths yet. Save them there, then run: systemctl reload caddy"
    fi
  elif [ "$cert_mode" = "le" ]; then
    behind_cf="n"
    if port_in_use 80; then
      warn "port 80 is already in use on this host — Let's Encrypt's HTTP challenge may fail. If the domain is behind Cloudflare, use Origin Certificate instead."
    fi
    [ -n "$email" ] || email="admin@${domain}"
    while :; do
      ask email "Email for Let's Encrypt" "for expiry notices and account recovery" "$email"
      valid_email "$email" && break
      warn "that doesn't look like an email address"
    done
  fi

  if ! have caddy; then
    if confirm "Caddy (the TLS front end) isn't installed. Install it now?" y; then
      install_caddy_pkg || { warn "Caddy install failed; set up TLS manually to 127.0.0.1:8080"; return; }
    else
      warn "skipping Caddy; reverse-proxy your own TLS to 127.0.0.1:8080"; return
    fi
  fi

  mkdir -p "$(dirname "$CADDYFILE")"
  if [ -s "$CADDYFILE" ]; then
    # Existing config — never overwrite. Back up, drop any previous Bumshi block,
    # then append the fresh one between markers.
    local bak="${CADDYFILE}.bumshi-bak.$(date +%Y%m%d%H%M%S)"
    cp "$CADDYFILE" "$bak"
    sed -i "\\|^${MARK_BEGIN}\$|,\\|^${MARK_END}\$|d" "$CADDYFILE"
    # Trim trailing blank lines so repeated runs never accumulate them.
    awk 'NF{last=NR} {line[NR]=$0} END{for (i = 1; i <= last; i++) print line[i]}' "$CADDYFILE" >"${CADDYFILE}.tmp" &&
      mv "${CADDYFILE}.tmp" "$CADDYFILE"
    printf '\n' >>"$CADDYFILE"
    emit_site_block >>"$CADDYFILE"
    ok "added a Bumshi site block to ${CADDYFILE} (backup: ${bak})"
  else
    {
      if [ "$cert_mode" = "le" ] && [ -n "$email" ]; then
        printf '{\n\temail %s\n}\n\n' "$email"
      fi
      emit_site_block
    } >"$CADDYFILE"
    ok "wrote ${CADDYFILE}"
  fi

  if have caddy && caddy validate --config "$CADDYFILE" >/dev/null 2>&1; then
    systemctl reload caddy 2>/dev/null || systemctl restart caddy 2>/dev/null ||
      warn "could not reload Caddy; check: systemctl status caddy"
    ok "Caddy configured for ${domain}"
  else
    warn "Caddy config did not validate — check it, then: systemctl reload caddy"
  fi
}

summary() {
  section "Done" "Bumshi is installed and running."
  local url
  if [ -n "$domain" ] && [ "${cert_mode:-skip}" != "skip" ]; then url="https://${domain}"; else url="http://<server-ip>:8080"; fi
  echo -e "  ${c_bold}Panel${c_reset}    ${url}${admin_path}"
  echo -e "  ${c_bold}Login${c_reset}    ${admin_user}"
  if [ "${gen_note:-0}" = "1" ]; then
    echo -e "  ${c_bold}Password${c_reset} ${c_yellow}${admin_pass}${c_reset}  ${c_dim}(shown once — save it now)${c_reset}"
  fi
  echo -e "  ${c_bold}Manage${c_reset}   bumshi menu   ${c_dim}(status / logs / restart / update)${c_reset}"
  if [ "${behind_cf:-n}" = "y" ]; then
    echo
    echo -e "  ${c_dim}Cloudflare: point ${domain} (proxied / orange cloud) at this server's IP,"
    echo -e "  and set SSL/TLS mode to Full (strict).${c_reset}"
  fi
  echo
}

main() {
  require_root
  section "Bumshi installer" "Self-hosted, censorship-resistant web proxy."

  local arch tag
  arch="$(detect_arch)"
  info "target: linux/${arch}"

  tag="$VERSION"
  if [ "$tag" = "latest" ]; then
    tag="$(resolve_latest || true)"
    [ -n "$tag" ] || die "could not resolve the latest release; set BUMSHI_VERSION=vX.Y.Z"
  fi

  # Config, seeded from the environment for non-interactive installs.
  domain="$(normalize_domain "${BUMSHI_DOMAIN:-}")"
  email="${BUMSHI_EMAIL:-}"
  admin_user="${BUMSHI_ADMIN_USERNAME:-admin}"
  admin_pass="${BUMSHI_ADMIN_PASSWORD:-}"
  admin_path="${BUMSHI_ADMIN_PATH:-/admin/}"
  public_url="${BUMSHI_PUBLIC_URL:-}"
  enable_proxy="${BUMSHI_ENABLE_PROXY:-true}"
  cert_mode="skip"
  behind_cf="n"
  gen_note=0

  case "${BUMSHI_TLS:-}" in
    cloudflare | cf | origin) cert_mode="cf"; cf_cert="${BUMSHI_TLS_CERT:-}"; cf_key="${BUMSHI_TLS_KEY:-}" ;;
    letsencrypt | le | acme) cert_mode="le" ;;
    none | skip) cert_mode="skip" ;;
  esac

  local reconfigure="y"
  if [ -f "$ENV_FILE" ] && interactive; then
    confirm "Existing config found at ${ENV_FILE}. Reconfigure it?" n || reconfigure="n"
  fi

  section "Installing" "Binary, service user, systemd unit, and management command."
  install_binary "$tag" "$arch"
  ensure_user
  install_unit
  install_mgmt

  if [ "$reconfigure" = "y" ]; then
    section "Configuration" "One question at a time. Press Enter to accept the [default]."

    while :; do
      ask domain "Domain" "the public hostname users connect to. Leave blank to run behind your own reverse proxy." "$domain"
      domain="$(normalize_domain "$domain")"
      [ -z "$domain" ] && break
      valid_domain "$domain" && break
      warn "that doesn't look like a domain (example: proxy.example.com)"
    done

    if interactive; then
      confirm "Enable the web proxy engine now (serve /p/)?" y && enable_proxy="true" || enable_proxy="false"
      echo
    fi

    ask admin_user "Admin username" "for signing into the admin panel" "$admin_user"

    if [ -z "$admin_pass" ] && interactive; then
      ask_secret admin_pass "Admin password" "leave blank to auto-generate a strong one"
    fi
    if [ -z "$admin_pass" ]; then admin_pass="$(gen_password)"; gen_note=1; fi

    ask admin_path "Admin panel path" "keep it secret and hard to guess (not /admin). It is normalized to /name/." "$admin_path"
    admin_path="$(normalize_path "$admin_path")"

    # Public URL is derived from the domain; only ask when there is no domain.
    if [ -n "$domain" ]; then
      public_url="https://${domain}"
    elif [ -z "$public_url" ]; then
      ask public_url "Public URL for connection links" "the full base URL the app connects to, e.g. https://proxy.example.com" ""
    fi

    write_env
    setup_tls
  else
    info "keeping existing configuration"
    # Re-read a couple of values so the summary is accurate.
    admin_user="$(grep -E '^BUMSHI_ADMIN_USERNAME=' "$ENV_FILE" 2>/dev/null | cut -d= -f2- || echo "$admin_user")"
    admin_path="$(grep -E '^BUMSHI_ADMIN_PATH=' "$ENV_FILE" 2>/dev/null | cut -d= -f2- || echo "$admin_path")"
    public_url="$(grep -E '^BUMSHI_PUBLIC_URL=' "$ENV_FILE" 2>/dev/null | cut -d= -f2- || echo "$public_url")"
    domain="$(normalize_domain "$public_url")"
    [ -n "$domain" ] && cert_mode="cf"
  fi

  systemctl enable --now bumshi
  summary
}

main "$@"
