#!/usr/bin/env sh
# install.sh — Install openclio for your platform.
# Usage: curl -sSL https://raw.githubusercontent.com/openclio/openclio/main/install.sh | sh

set -e

REPO="openclio/openclio"
BINARY="openclio"
INSTALL_DIR="${HOME}/.local/bin"

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

# ── Detect platform ───────────────────────────────────────────────────────────
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "${ARCH}" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)
    echo "✗ Unsupported architecture: ${ARCH}"
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
mkdir -p "${INSTALL_DIR}"
mv "${TMP_DIR}/${BINARY}-${VERSION}-${OS}-${ARCH}" "${INSTALL_DIR}/${BINARY}"
rm -rf "${TMP_DIR}"

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo "─────────────────────────────────────────────────────────────────────────"
echo ""
echo "  ✓ openclio ${VERSION} installed successfully!"
echo ""
echo "  Next steps:"
echo "    1. Set your API key    →  export ANTHROPIC_API_KEY=\"sk-ant-...\""
echo "    2. Run setup wizard    →  openclio init"
echo "    3. Start chatting      →  openclio"
echo ""

# PATH hint
if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
  echo "  Note: Add ${INSTALL_DIR} to your PATH:"
  echo "    echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ~/.zshrc && source ~/.zshrc"
  echo ""
fi

echo "─────────────────────────────────────────────────────────────────────────"
echo ""
