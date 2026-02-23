#!/bin/bash
# openclio Installer Script
# One-liner: curl -sSL https://get.openclio.dev/install.sh | sh
set -e

REPO="openclio/distribution"
BINARY_NAME="openclio"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${VERSION:-latest}"

detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)
    
    case "$OS" in
        linux) OS="linux" ;;
        darwin) OS="darwin" ;;
        mingw*|msys*|cygwin*) OS="windows" ;;
        *) echo "Unsupported OS: $OS"; exit 1 ;;
    esac
    
    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        *) echo "Unsupported arch: $ARCH"; exit 1 ;;
    esac
    
    PLATFORM="${OS}-${ARCH}"
}

get_latest_version() {
    if [ "$VERSION" = "latest" ]; then
        VERSION=$(curl -sL "https://api.github.com/repos/${REPO}/releases/latest" | \
            grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    fi
    echo "Installing openclio $VERSION..."
}

download_binary() {
    if [ "$OS" = "windows" ]; then
        FILENAME="${BINARY_NAME}-${VERSION}-${PLATFORM}.zip"
    else
        FILENAME="${BINARY_NAME}-${VERSION}-${PLATFORM}.tar.gz"
    fi
    
    URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"
    TMP=$(mktemp -d)
    
    echo "→ Downloading $FILENAME..."
    curl -sSL "$URL" -o "$TMP/$FILENAME"
    
    cd "$TMP"
    if [ "$OS" = "windows" ]; then
        unzip -q "$FILENAME"
    else
        tar -xzf "$FILENAME"
    fi
    
    if [ -w "$INSTALL_DIR" ]; then
        mv "${BINARY_NAME}" "${INSTALL_DIR}/"
    else
        sudo mv "${BINARY_NAME}" "${INSTALL_DIR}/"
    fi
    
    rm -rf "$TMP"
}

main() {
    detect_platform
    get_latest_version
    download_binary
    
    echo "✓ openclio installed to $(command -v openclio)"
    echo "  Run: openclio init"
}

main
