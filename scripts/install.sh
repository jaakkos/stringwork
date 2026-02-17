#!/usr/bin/env sh
# Install or update mcp-stringwork.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/jaakkos/stringwork/main/scripts/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/jaakkos/stringwork/main/scripts/install.sh | sh -s -- --dir /usr/local/bin
#   curl -fsSL https://raw.githubusercontent.com/jaakkos/stringwork/main/scripts/install.sh | sh -s -- --version v0.1.0

set -e

REPO="jaakkos/stringwork"
BINARY_NAME="mcp-stringwork"
INSTALL_DIR="${HOME}/.local/bin"
VERSION=""

# Parse args
while [ $# -gt 0 ]; do
  case "$1" in
    --dir)      INSTALL_DIR="$2"; shift 2 ;;
    --version)  VERSION="$2"; shift 2 ;;
    --help|-h)
      echo "Usage: install.sh [--dir PATH] [--version TAG]"
      echo ""
      echo "  --dir PATH      Install directory (default: ~/.local/bin)"
      echo "  --version TAG   Specific version tag (default: latest)"
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

detect_os() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    linux*)  echo "linux" ;;
    darwin*) echo "darwin" ;;
    *)       echo "Unsupported OS: $os" >&2; exit 1 ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64)   echo "amd64" ;;
    aarch64|arm64)   echo "arm64" ;;
    *)               echo "Unsupported architecture: $arch" >&2; exit 1 ;;
  esac
}

latest_version() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4
  else
    echo "Error: curl or wget required" >&2
    exit 1
  fi
}

download() {
  url="$1"
  dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$dest" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" "$url"
  fi
}

# --- Main ---

OS="$(detect_os)"
ARCH="$(detect_arch)"

if [ -z "$VERSION" ]; then
  echo "Fetching latest version..."
  VERSION="$(latest_version)"
fi

if [ -z "$VERSION" ]; then
  echo "Error: could not determine version. Pass --version TAG or check https://github.com/${REPO}/releases" >&2
  exit 1
fi

ARTIFACT="${BINARY_NAME}-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARTIFACT}"
CHECKSUM_URL="${URL}.sha256"

echo "Installing ${BINARY_NAME} ${VERSION} (${OS}/${ARCH})"
echo "  From: ${URL}"
echo "  To:   ${INSTALL_DIR}/${BINARY_NAME}"

# Download binary
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

download "$URL" "${TMPDIR}/${ARTIFACT}"

# Verify checksum
if download "$CHECKSUM_URL" "${TMPDIR}/${ARTIFACT}.sha256" 2>/dev/null; then
  cd "$TMPDIR"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c "${ARTIFACT}.sha256" >/dev/null 2>&1 && echo "  Checksum: OK" || echo "  Warning: checksum mismatch"
  elif command -v shasum >/dev/null 2>&1; then
    expected="$(cut -d' ' -f1 "${ARTIFACT}.sha256")"
    actual="$(shasum -a 256 "${ARTIFACT}" | cut -d' ' -f1)"
    if [ "$expected" = "$actual" ]; then
      echo "  Checksum: OK"
    else
      echo "  Warning: checksum mismatch"
    fi
  fi
  cd - >/dev/null
fi

# Install
mkdir -p "$INSTALL_DIR"
mv "${TMPDIR}/${ARTIFACT}" "${INSTALL_DIR}/${BINARY_NAME}"
chmod +x "${INSTALL_DIR}/${BINARY_NAME}"

# Create config directory
mkdir -p "${HOME}/.config/stringwork"

echo ""
echo "Installed ${BINARY_NAME} ${VERSION} to ${INSTALL_DIR}/${BINARY_NAME}"

# Check PATH
case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    echo ""
    echo "Add ${INSTALL_DIR} to your PATH:"
    echo ""
    echo "  echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ~/.bashrc   # bash"
    echo "  echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ~/.zshrc    # zsh"
    ;;
esac

echo ""
echo "Verify:  ${BINARY_NAME} --help"
echo "Config:  ~/.config/stringwork/config.yaml"
