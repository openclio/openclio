#!/bin/bash
# build-full-package.sh — Create full distribution with binary + documentation

set -e

VERSION=${1:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}
BINARY_NAME="openclio"
DIST_DIR="dist"

echo "Building full package for version: $VERSION"

# Build binary first
make build-all

# Create full package for each platform
for platform in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64; do
    echo "Packaging for $platform..."
    
    PKG_DIR="$DIST_DIR/$BINARY_NAME-$VERSION-$platform-full"
    mkdir -p "$PKG_DIR"
    
    # Copy binary
    cp "bin/$BINARY_NAME-$platform" "$PKG_DIR/$BINARY_NAME" 2>/dev/null || \
    cp "bin/$BINARY_NAME-$platform.exe" "$PKG_DIR/$BINARY_NAME.exe" 2>/dev/null || true
    
    # Copy documentation
    mkdir -p "$PKG_DIR/docs"
    cp SOUL.md "$PKG_DIR/docs/"
    cp VISION.md "$PKG_DIR/docs/"
    cp AGENTS.md "$PKG_DIR/docs/"
    cp README.md "$PKG_DIR/docs/"
    cp CHANGELOG.md "$PKG_DIR/docs/"
    cp SECURITY.md "$PKG_DIR/docs/"
    
    # Copy example configs
    cp config.example.yaml "$PKG_DIR/config.example.yaml"
    
    # Create install script
    cat > "$PKG_DIR/install.sh" << 'INSTALL_SCRIPT'
#!/bin/bash
# Install openclio with full documentation

set -e

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Installing openclio..."

# Install binary
if [ -f "$SCRIPT_DIR/openclio" ]; then
    sudo install -m 755 "$SCRIPT_DIR/openclio" "$INSTALL_DIR/openclio"
elif [ -f "$SCRIPT_DIR/openclio.exe" ]; then
    echo "Windows binary found. Please manually copy to your PATH."
    exit 1
fi

# Create data directory with documentation
DATA_DIR="${HOME}/.openclio"
mkdir -p "$DATA_DIR"

# Copy documentation for reference
if [ -d "$SCRIPT_DIR/docs" ]; then
    cp -r "$SCRIPT_DIR/docs" "$DATA_DIR/"
    echo "Documentation installed to $DATA_DIR/docs/"
fi

# Copy example config
if [ -f "$SCRIPT_DIR/config.example.yaml" ]; then
    cp "$SCRIPT_DIR/config.example.yaml" "$DATA_DIR/config.example.yaml"
fi

echo "✓ Installation complete!"
echo ""
echo "Next steps:"
echo "  1. Run: openclio init"
echo "  2. Set your API key (e.g., export ANTHROPIC_API_KEY=...)"
echo "  3. Start chatting: openclio chat"
echo ""
echo "Documentation available at: $DATA_DIR/docs/"
INSTALL_SCRIPT
    chmod +x "$PKG_DIR/install.sh"
    
    # Create README for package
    cat > "$PKG_DIR/PACKAGE_README.txt" << EOF
openclio $VERSION — Complete Distribution

This package includes:
  • openclio binary — The AI agent executable
  • docs/ — Full documentation (SOUL.md, VISION.md, AGENTS.md, etc.)
  • config.example.yaml — Example configuration
  • install.sh — Installation script

Quick Install:
  ./install.sh

Manual Install:
  1. Copy 'openclio' to your PATH (e.g., /usr/local/bin)
  2. Run: openclio init
  3. Start using!

Documentation:
  • docs/SOUL.md — Agent personality and values
  • docs/VISION.md — Project philosophy and roadmap
  • docs/AGENTS.md — Developer reference
  • docs/README.md — User guide
  • docs/SECURITY.md — Security information

For more: https://github.com/openclio/openclio
EOF

    # Create tarball
    tar -czf "$DIST_DIR/$BINARY_NAME-$VERSION-$platform-full.tar.gz" -C "$DIST_DIR" "$BINARY_NAME-$VERSION-$platform-full"
    
    # Cleanup
    rm -rf "$PKG_DIR"
    
    echo "  ✓ Created $BINARY_NAME-$VERSION-$platform-full.tar.gz"
done

echo ""
echo "Full packages created in $DIST_DIR/"
ls -lh "$DIST_DIR/"*-full.tar.gz
