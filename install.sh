#!/bin/sh
# Install PromptRail
# Usage: curl -fsSL https://raw.githubusercontent.com/ashmaster/promptrail/main/install.sh | sh

set -e

REPO="ashmaster/promptrail"
BINARY="pt"
INSTALL_DIR="/usr/local/bin"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
    linux|darwin) ;;
    *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Get latest version
VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed 's/.*"v\(.*\)".*/\1/')

if [ -z "$VERSION" ]; then
    echo "Error: Could not determine latest version"
    exit 1
fi

echo "Installing PromptRail v${VERSION} (${OS}/${ARCH})..."

# Download
FILENAME="promptrail_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/v${VERSION}/${FILENAME}"

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

curl -fsSL "$URL" -o "$TMPDIR/$FILENAME"
tar -xzf "$TMPDIR/$FILENAME" -C "$TMPDIR"

# Install
if [ -w "$INSTALL_DIR" ]; then
    mv "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"
else
    echo "Need sudo to install to $INSTALL_DIR"
    sudo mv "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"
fi

chmod +x "$INSTALL_DIR/$BINARY"

echo "Installed PromptRail v${VERSION} to $INSTALL_DIR/$BINARY"
echo ""
echo "Get started:"
echo "  pt login          # authenticate with GitHub"
echo "  pt list           # browse local sessions"
echo "  pt upload         # upload a session"
echo "  pt view <id>      # view a session"
