#!/usr/bin/env sh
# install.sh — Install the agent binary for your platform.
# Usage: curl -sSL https://raw.githubusercontent.com/USER/REPO/main/install.sh | sh

set -e

REPO="user/agent"
INSTALL_DIR="${HOME}/.local/bin"
BINARY="agent"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "${ARCH}" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: ${ARCH}"
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

ASSET="${BINARY}-${OS}-${ARCH}"

# Get latest release version
echo "Fetching latest release..."
VERSION=$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | sed 's/.*"tag_name": "\(.*\)".*/\1/')

if [ -z "${VERSION}" ]; then
  echo "Error: Could not determine latest version"
  exit 1
fi

echo "Installing agent ${VERSION} (${OS}/${ARCH})..."

# Download binary
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
TMP=$(mktemp)

if ! curl -sSfL "${URL}" -o "${TMP}"; then
  echo "Error: Failed to download ${URL}"
  rm -f "${TMP}"
  exit 1
fi

# Install
mkdir -p "${INSTALL_DIR}"
chmod +x "${TMP}"
mv "${TMP}" "${INSTALL_DIR}/${BINARY}"

echo ""
echo "✓ agent ${VERSION} installed to ${INSTALL_DIR}/${BINARY}"

# PATH hint
if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
  echo ""
  echo "Add the following to your shell profile (~/.bashrc or ~/.zshrc):"
  echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
fi
