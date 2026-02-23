#!/usr/bin/env sh
# install.sh — Install openclio for your platform.
# Usage: curl -sSL https://raw.githubusercontent.com/openclio/openclio/main/install.sh | sh

set -e

REPO="openclio/openclio"
BINARY="openclio"
INSTALL_DIR="${HOME}/.local/bin"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "${ARCH}" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: ${ARCH}"
    echo "Please download manually from: https://github.com/${REPO}/releases"
    exit 1
    ;;
esac

case "${OS}" in
  linux|darwin) ;;
  *)
    echo "Unsupported OS: ${OS}"
    echo "Please download manually from: https://github.com/${REPO}/releases"
    exit 1
    ;;
esac

# Get latest release version
echo "Fetching latest openclio release..."
VERSION=$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | sed 's/.*"tag_name": "\(.*\)".*/\1/')

if [ -z "${VERSION}" ]; then
  echo "Error: Could not determine latest version."
  echo "Check https://github.com/${REPO}/releases for available versions."
  exit 1
fi

echo "Installing openclio ${VERSION} (${OS}/${ARCH})..."

ARCHIVE="${BINARY}-${VERSION}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
TMP_DIR=$(mktemp -d)

# Download archive
if ! curl -sSfL "${URL}" -o "${TMP_DIR}/${ARCHIVE}"; then
  echo "Error: Failed to download ${URL}"
  echo "Please download manually from: https://github.com/${REPO}/releases"
  rm -rf "${TMP_DIR}"
  exit 1
fi

# Extract binary
tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "${TMP_DIR}"
chmod +x "${TMP_DIR}/${BINARY}-${VERSION}-${OS}-${ARCH}"

# Install
mkdir -p "${INSTALL_DIR}"
mv "${TMP_DIR}/${BINARY}-${VERSION}-${OS}-${ARCH}" "${INSTALL_DIR}/${BINARY}"
rm -rf "${TMP_DIR}"

echo ""
echo "✓ openclio ${VERSION} installed to ${INSTALL_DIR}/${BINARY}"
echo ""
echo "Next steps:"
echo "  1. Set your API key:   export ANTHROPIC_API_KEY=\"sk-ant-...\""
echo "  2. Run setup wizard:   openclio init"
echo "  3. Start chatting:     openclio"
echo ""

# PATH hint
if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
  echo "Note: ${INSTALL_DIR} is not in your PATH."
  echo "Add this to your ~/.bashrc or ~/.zshrc:"
  echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
  echo ""
fi
