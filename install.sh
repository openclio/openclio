#!/usr/bin/env sh
# install.sh — Install openclio for your platform.
# Usage: curl -sSL https://raw.githubusercontent.com/openclio/openclio/main/install.sh | sh

set -e

REPO="openclio/openclio"
BINARY="openclio"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
RAW_ARCH=$(uname -m)

case "${RAW_ARCH}" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)
    echo "✗ Unsupported architecture: ${RAW_ARCH}"
    echo "  Download manually: https://github.com/${REPO}/releases"
    exit 1
    ;;
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

# ── Fetch latest version ──────────────────────────────────────────────────────
echo "  Fetching latest release..."
VERSION=$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | sed 's/.*"tag_name": "\(.*\)".*/\1/')

if [ -z "${VERSION}" ]; then
  echo ""
  echo "✗ Could not determine latest version."
  echo "  Check: https://github.com/${REPO}/releases"
  exit 1
fi

echo "  Version  : ${VERSION}"
echo "  Platform : ${OS}/${ARCH}"
echo "  Install  : ${INSTALL_DIR}/${BINARY}"
echo ""

# ── Download ──────────────────────────────────────────────────────────────────
ARCHIVE="${BINARY}-${VERSION}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
TMP_DIR=$(mktemp -d)

echo "  Downloading..."
if ! curl -sSfL "${URL}" -o "${TMP_DIR}/${ARCHIVE}"; then
  echo ""
  echo "✗ Download failed: ${URL}"
  echo "  Download manually: https://github.com/${REPO}/releases"
  rm -rf "${TMP_DIR}"
  exit 1
fi

# ── Extract & install ─────────────────────────────────────────────────────────
echo "  Installing..."
tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "${TMP_DIR}"
chmod +x "${TMP_DIR}/${BINARY}-${VERSION}-${OS}-${ARCH}"
if [ "${USE_SUDO}" -eq 1 ]; then
  sudo mkdir -p "${INSTALL_DIR}"
  sudo install -m 0755 "${TMP_DIR}/${BINARY}-${VERSION}-${OS}-${ARCH}" "${INSTALL_DIR}/${BINARY}"
else
  mkdir -p "${INSTALL_DIR}"
  install -m 0755 "${TMP_DIR}/${BINARY}-${VERSION}-${OS}-${ARCH}" "${INSTALL_DIR}/${BINARY}"
fi
rm -rf "${TMP_DIR}"

if [ ! -x "${INSTALL_DIR}/${BINARY}" ]; then
  echo ""
  echo "✗ Install failed: ${INSTALL_DIR}/${BINARY} is missing or not executable"
  exit 1
fi

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo "─────────────────────────────────────────────────────────────────────────"
echo ""
echo "  ✓ openclio ${VERSION} installed successfully!"
echo ""
echo "  Next steps:"
echo "    1. Run setup wizard    →  openclio init"
echo "    2. Choose provider     →  Select Ollama/OpenAI/Anthropic/Gemini in the wizard"
echo "    3. Set credentials     →  Use the env var shown by the wizard (if required)"
echo "    4. Start chatting      →  openclio"
echo ""

# PATH hint
if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
  echo "  Note: Add ${INSTALL_DIR} to your PATH:"
  echo "    echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ~/.zshrc && source ~/.zshrc"
  echo "  Or run directly now:"
  echo "    ${INSTALL_DIR}/${BINARY} init"
  echo ""
fi

echo "─────────────────────────────────────────────────────────────────────────"
echo ""
