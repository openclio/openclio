#!/usr/bin/env sh
# install.sh — friendly installer for openclio
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/openclio/openclio/main/install.sh | sh

set -eu

REPO="${OPENCLIO_RELEASE_REPO:-openclio/openclio}"
BINARY="openclio"
EDITION="${OPENCLIO_EDITION:-community}"

# Defaults (can be overridden with env vars)
DEFAULT_INSTALL_DIR="/usr/local/bin"
INSTALL_DIR="${OPENCLIO_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"

# Color helpers (no color if not a terminal)
if [ -t 1 ]; then
  RED='\033[0;31m'
  GREEN='\033[0;32m'
  YELLOW='\033[0;33m'
  BLUE='\033[0;34m'
  BOLD='\033[1m'
  RESET='\033[0m'
else
  RED='' GREEN='' YELLOW='' BLUE='' BOLD='' RESET=''
fi

info()   { printf "%b\n" "${BLUE}➜${RESET} $*"; }
ok()     { printf "%b\n" "${GREEN}✓${RESET} $*"; }
warn()   { printf "%b\n" "${YELLOW}⚠${RESET} $*"; }
err()    { printf "%b\n" "${RED}✗${RESET} $*" >&2; }

usage() {
  cat <<EOF
Usage: install.sh [--version <tag>] [--install-dir <path>]

Installs the ${BINARY} binary into a standard location.
Options:
  --version     Release tag to install (default: latest)
  --install-dir Custom installation directory (default: ${INSTALL_DIR})
EOF
  exit 1
}

# Parse args (simple)
VERSION=""
SKIP_INIT=0
while [ "${#}" -gt 0 ]; do
  case "$1" in
    --version) shift; VERSION="$1"; shift ;;
    --install-dir) shift; INSTALL_DIR="$1"; shift ;;
    --no-init) shift; SKIP_INIT=1 ;;
    --help|-h) usage ;;
    *) err "Unknown arg: $1"; usage ;;
  esac
done

if [ "${EDITION}" = "enterprise" ] && [ -z "${OPENCLIO_RELEASE_REPO:-}" ]; then
  err "Enterprise install requires OPENCLIO_RELEASE_REPO (for example: your-org/openclio-enterprise)."
  exit 1
fi

# Platform detection
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
RAW_ARCH="$(uname -m)"
case "${RAW_ARCH}" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7l|armv7)  ARCH="armv7" ;;
  *) err "Unsupported architecture: ${RAW_ARCH}"; exit 1 ;;
esac
case "${OS}" in
  linux|darwin) ;; 
  *) err "Unsupported OS: ${OS}"; exit 1 ;;
esac

case "${OS}" in
  linux|darwin) ;;
  *)
    echo "✗ Unsupported OS: ${OS}"
    echo "  Download manually: https://github.com/${REPO}/releases"
    exit 1
    ;;
esac

# Prefer system-wide install paths so the binary is available from standard PATH.
# Users can override with: OPENCLIO_INSTALL_DIR=/custom/path
DEFAULT_INSTALL_DIR="/usr/local/bin"
if [ "${OS}" = "darwin" ] && [ "${ARCH}" = "arm64" ] && [ -d "/opt/homebrew/bin" ]; then
  DEFAULT_INSTALL_DIR="/opt/homebrew/bin"
fi
INSTALL_DIR="${OPENCLIO_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"
USE_SUDO=0

# ── Banner ────────────────────────────────────────────────────────────────────
echo ""
echo "  ██████╗ ██████╗ ███████╗███╗   ██╗     ██████╗██╗     ██╗ ██████╗ "
echo " ██╔═══██╗██╔══██╗██╔════╝████╗  ██║    ██╔════╝██║     ██║██╔═══██╗"
echo " ██║   ██║██████╔╝█████╗  ██╔██╗ ██║    ██║     ██║     ██║██║   ██║"
echo " ██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║    ██║     ██║     ██║██║   ██║"
echo " ╚██████╔╝██║     ███████╗██║ ╚████║    ╚██████╗███████╗██║╚██████╔╝"
echo "  ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝     ╚═════╝╚══════╝╚═╝ ╚═════╝ "
echo ""
echo "  Local-first personal AI agent — single binary, no cloud, no telemetry"
echo "  https://github.com/openclio/openclio"
echo ""
echo "─────────────────────────────────────────────────────────────────────────"
echo ""

# ── Resolve install permissions ───────────────────────────────────────────────
if [ -z "${OPENCLIO_INSTALL_DIR}" ] && [ ! -w "${INSTALL_DIR}" ]; then
  if command -v sudo >/dev/null 2>&1; then
    USE_SUDO=1
  else
    INSTALL_DIR="${HOME}/.local/bin"
    echo "  Note: system install path is not writable and sudo is unavailable."
    echo "        Falling back to user install path: ${INSTALL_DIR}"
  fi
fi

# ── Resolve version ───────────────────────────────────────────────────────────
if [ -z "${VERSION}" ]; then
  echo "  Fetching latest release..."
  VERSION=$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | sed 's/.*"tag_name": "\(.*\)".*/\1/')
fi

if [ -z "${VERSION}" ]; then
  echo ""
  echo "✗ Could not determine latest version."
  echo "  Check: https://github.com/${REPO}/releases"
  exit 1
fi

echo "  Version  : ${VERSION}"
echo "  Edition  : ${EDITION}"
echo "  Source   : ${REPO}"
echo "  Platform : ${OS}/${ARCH}"
echo "  Install  : ${INSTALL_DIR}/${BINARY}"
echo ""

# ── Download ──────────────────────────────────────────────────────────────────
ARCHIVE="${BINARY}-${VERSION}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
TMP_DIR=$(mktemp -d)

info "Downloading ${ARCHIVE}..."
if ! curl -fsSL "${URL}" -o "${TMP_DIR}/${ARCHIVE}"; then
  err "Download failed: ${URL}"
  err "Download manually: https://github.com/${REPO}/releases"
  rm -rf "${TMP_DIR}"
  exit 1
fi
ok "Downloaded ${ARCHIVE}"

info "Extracting archive..."
if ! tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "${TMP_DIR}"; then
  err "Failed to extract archive"
  rm -rf "${TMP_DIR}"
  exit 1
fi
ok "Archive extracted"

info "Installing to ${INSTALL_DIR}..."
if [ "${USE_SUDO}" -eq 1 ]; then
  sudo mkdir -p "${INSTALL_DIR}"
  sudo install -m 0755 "${TMP_DIR}/${BINARY}-${VERSION}-${OS}-${ARCH}" "${INSTALL_DIR}/${BINARY}"
else
  mkdir -p "${INSTALL_DIR}"
  install -m 0755 "${TMP_DIR}/${BINARY}-${VERSION}-${OS}-${ARCH}" "${INSTALL_DIR}/${BINARY}"
fi
ok "Installed ${INSTALL_DIR}/${BINARY}"

if [ ! -x "${INSTALL_DIR}/${BINARY}" ]; then
  err "Install failed: ${INSTALL_DIR}/${BINARY} is missing or not executable"
  exit 1
fi


# ── Done ──────────────────────────────────────────────────────────────────────
printf "\n"
echo "─────────────────────────────────────────────────────────────────────────"
printf "\n"
ok "openclio ${VERSION} installed successfully!"
printf "\n"

# Ensure install dir is on PATH hint
if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
  warn "Add ${INSTALL_DIR} to your PATH to run 'openclio' directly."
  info "  echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ~/.zshrc && source ~/.zshrc"
  printf "\n"
fi

# By default run setup and start the server automatically.
START_AFTER_INSTALL=1
if [ "${SKIP_INIT:-0}" -eq 0 ]; then
  info "Starting interactive setup wizard now — this will guide you to select a model and configure providers/accounts."
  # Run the init command from the installed location so it's deterministic.
  "${INSTALL_DIR}/${BINARY}" init
  ok "Setup complete."
else
  echo ""
  echo "Next steps:"
  echo "  1) Run setup wizard: ${INSTALL_DIR}/${BINARY} init"
fi

# If requested, start the server in background and open the web UI.
if [ "${START_AFTER_INSTALL}" -eq 1 ]; then
  DATA_DIR="${HOME}/.openclio"
  PID_FILE="${DATA_DIR}/openclio.pid"
  LOG_FILE="${DATA_DIR}/openclio.log"
  mkdir -p "${DATA_DIR}"

  info "Starting openclio server in background..."
  nohup "${INSTALL_DIR}/${BINARY}" serve >> "${LOG_FILE}" 2>&1 &
  echo $! > "${PID_FILE}"
  sleep 1

  # Wait for auth token to appear (server writes it on startup)
  TOKEN_FILE="${DATA_DIR}/auth.token"
  i=0
  TOKEN=""
  while [ "${i}" -lt 20 ]; do
    if [ -f "${TOKEN_FILE}" ]; then
      TOKEN="$(tr -d '[:space:]' < "${TOKEN_FILE}")" || true
      break
    fi
    i=$((i+1))
    sleep 0.5
  done

  UI_URL="http://127.0.0.1:18789"
  if [ -n "${TOKEN}" ]; then
    UI_URL="${UI_URL}/?token=${TOKEN}"
  fi

  info "Opening web UI: ${UI_URL}"
  case "$(uname -s)" in
    Darwin) open "${UI_URL}" ;;
    Linux) xdg-open "${UI_URL}" >/dev/null 2>&1 || true ;;
    *) printf "Open your browser at: %s\n" "${UI_URL}" ;;
  esac

  ok "Server started (pid $(cat "${PID_FILE}")) — logs: ${LOG_FILE}"
else
  info "Server start skipped. Use: ${INSTALL_DIR}/${BINARY} serve"
fi

echo "─────────────────────────────────────────────────────────────────────────"
echo ""

echo "─────────────────────────────────────────────────────────────────────────"
echo ""
