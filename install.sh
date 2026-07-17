#!/usr/bin/env bash
set -eu
# pipefail is bash-only; silently skip when running under sh (dash)
(set -o pipefail) 2>/dev/null && set -o pipefail

REPO="happyTonakai/PaperAgent"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
INSTALL_DIR="${INSTALL_DIR%/}"  # strip trailing slash so PATH match works

# ── Detect OS & arch ──────────────────────────────────────────────
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$OS" in
  darwin)  GOOS="darwin"  ;;
  linux)   GOOS="linux"   ;;
  mingw*|msys*|cygwin*)
    echo "Detected Windows-like shell environment ($OS)." >&2
    echo "On Windows, please run the PowerShell script instead:" >&2
    echo "" >&2
    echo "  powershell -c \"irm https://raw.githubusercontent.com/$REPO/main/install.ps1 | iex\"" >&2
    echo "" >&2
    echo "Or download the binary manually from:" >&2
    echo "  https://github.com/$REPO/releases/latest" >&2
    exit 1
    ;;
  *)
    echo "Unsupported OS: $OS (only darwin and linux are supported)" >&2
    exit 1
    ;;
esac

case "$ARCH" in
  x86_64|amd64) GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH (only amd64 and arm64 are supported)" >&2
    exit 1
    ;;
esac

# ── Validate against the actual build matrix ─────────────────────
# release.yml currently ships darwin/arm64 + linux/amd64 only.
# Reject combos that would 404 at download time with a helpful hint.
case "$GOOS:$GOARCH" in
  darwin:arm64|linux:amd64) ;;
  darwin:amd64)
    echo "PaperAgent does not publish an Intel Mac binary (current releases are Apple Silicon only)." >&2
    echo "Build from source: https://github.com/$REPO#开发" >&2
    exit 1
    ;;
  linux:arm64)
    echo "PaperAgent does not publish an ARM Linux binary (Raspberry Pi etc.)." >&2
    echo "Build from source: https://github.com/$REPO#开发" >&2
    exit 1
    ;;
  *)
    echo "No published binary for $GOOS/$GOARCH." >&2
    exit 1
    ;;
esac

# ── Resolve the download URL ─────────────────────────────────────
# If VERSION is set (e.g. "v1.0.0"), download that specific release;
# otherwise fetch the latest release.
if [ -n "${VERSION:-}" ]; then
  TAG="$VERSION"
else
  echo "Detecting latest release..." >&2
  TAG=$(curl -sSfL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name":' \
    | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
  if [ -z "$TAG" ]; then
    echo "Failed to detect the latest release tag." >&2
    exit 1
  fi
fi

# ── Download with atomic replace ──────────────────────────────
# Download to temp files first (same filesystem as the target so mv
# is atomic), then rename into place.  This avoids leaving a truncated
# binary if the download is interrupted.
mkdir -p "$INSTALL_DIR"

PAPERAGENT_TMP=$(mktemp "$INSTALL_DIR/.paperagent.XXXXXXXX")
ARXIV2MD_TMP=$(mktemp "$INSTALL_DIR/.arxiv2md.XXXXXXXX")

cleanup() {
  rm -f "$PAPERAGENT_TMP" "$ARXIV2MD_TMP"
}
trap cleanup EXIT INT TERM

BINARY="paperagent_${GOOS}_${GOARCH}"
URL="https://github.com/$REPO/releases/download/$TAG/$BINARY"
echo "Downloading $BINARY ($TAG)..." >&2
if ! curl -sSfL "$URL" -o "$PAPERAGENT_TMP"; then
  echo "" >&2
  echo "Download failed. Please check the actual asset name at:" >&2
  echo "  https://github.com/$REPO/releases/tag/$TAG" >&2
  exit 1
fi
chmod +x "$PAPERAGENT_TMP"

BINARY="arxiv2md_${GOOS}_${GOARCH}"
URL="https://github.com/$REPO/releases/download/$TAG/$BINARY"
echo "Downloading $BINARY ($TAG)..." >&2
if ! curl -sSfL "$URL" -o "$ARXIV2MD_TMP"; then
  echo "" >&2
  echo "Download failed. Please check the actual asset name at:" >&2
  echo "  https://github.com/$REPO/releases/tag/$TAG" >&2
  exit 1
fi
chmod +x "$ARXIV2MD_TMP"

# ── Verify temporary binaries before replacing ──────────────
echo "Verifying paperagent..." >&2
if ! "$PAPERAGENT_TMP" -version >/dev/null 2>&1; then
  echo "paperagent verification failed (downloaded binary may be corrupted)" >&2
  exit 1
fi

echo "Verifying arxiv2md..." >&2
arxiv2md_status=0
"$ARXIV2MD_TMP" >/dev/null 2>&1 || arxiv2md_status=$?
if [ "$arxiv2md_status" -ne 0 ] && [ "$arxiv2md_status" -ne 1 ]; then
  echo "arxiv2md verification failed (downloaded binary may be corrupted)" >&2
  exit 1
fi

mv -f "$PAPERAGENT_TMP" "$INSTALL_DIR/paperagent"
mv -f "$ARXIV2MD_TMP" "$INSTALL_DIR/arxiv2md"
trap - EXIT INT TERM

# ── Platform-specific post-install ───────────────────────────────
# macOS: clear Gatekeeper quarantine so the binary can run without
#        the user manually opening "Security & Privacy" once.
# Linux: surface any missing shared libs (modernc.org/sqlite is pure
#        Go so this is rarely an issue, but worth flagging early).
case "$GOOS" in
  darwin)
    if command -v xattr >/dev/null 2>&1; then
      xattr -cr "$INSTALL_DIR/paperagent" || true
      xattr -cr "$INSTALL_DIR/arxiv2md" || true
    fi
    ;;
  linux)
    if command -v ldd >/dev/null 2>&1; then
      if ldd "$INSTALL_DIR/paperagent" 2>/dev/null | grep -q "not found"; then
        echo "" >&2
        echo "⚠  Missing shared libraries detected:" >&2
        ldd "$INSTALL_DIR/paperagent" | grep "not found" >&2
      fi
    fi
    ;;
esac

# ── Done ──────────────────────────────────────────────────────────
echo "Installed paperagent to $INSTALL_DIR/paperagent" >&2

if ! echo ":$PATH:" | grep -q ":$INSTALL_DIR:" 2>/dev/null; then
  echo "" >&2
  echo "⚠  $INSTALL_DIR is not in your PATH." >&2
  echo "   Add it by running:" >&2
  echo "" >&2
  echo "   export PATH=\"$INSTALL_DIR:\$PATH\"" >&2
  echo "" >&2
  echo "   Or add that line to your ~/.bashrc / ~/.zshrc." >&2
fi

# ── Verify ────────────────────────────────────────────────────────
echo "" >&2
"$INSTALL_DIR/paperagent" -version
echo "" >&2
"$INSTALL_DIR/arxiv2md" --version
echo "Installed arxiv2md to $INSTALL_DIR/arxiv2md" >&2