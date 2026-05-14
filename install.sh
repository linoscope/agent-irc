#!/usr/bin/env sh
# install.sh — fetch the latest agent-irc binary release.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/linoscope/agent-irc/main/install.sh | sh
#
# Honored env vars:
#   PREFIX     install prefix; the binary lands in $PREFIX/bin (default: $HOME/.local)
#   VERSION    release tag to install, e.g. v0.1.0 (default: latest)
#   REPO       owner/name (default: linoscope/agent-irc)
set -eu

REPO="${REPO:-linoscope/agent-irc}"
PREFIX="${PREFIX:-$HOME/.local}"
VERSION="${VERSION:-latest}"

# -- OS / arch detection -------------------------------------------------------
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) echo "install.sh: unsupported OS: $os (need linux or darwin)" >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "install.sh: unsupported arch: $arch (need amd64 or arm64)" >&2; exit 1 ;;
esac

# -- resolve version -----------------------------------------------------------
if [ "$VERSION" = "latest" ]; then
  # Follow the redirect from /releases/latest to discover the tag without
  # depending on jq/curl-with-json.
  resolved=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/$REPO/releases/latest")
  VERSION=${resolved##*/}
  if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
    echo "install.sh: could not resolve latest release tag for $REPO" >&2
    echo "  hint: no releases published yet? try VERSION=vX.Y.Z" >&2
    exit 1
  fi
fi

# Tag is e.g. v0.1.0; goreleaser drops the leading v in Version.
ver_no_prefix=${VERSION#v}

tarball="agent-irc-${ver_no_prefix}-${os}-${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${VERSION}/${tarball}"

# -- download + extract --------------------------------------------------------
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

echo "agent-irc: downloading $tarball ..."
if ! curl -fsSL -o "$tmpdir/$tarball" "$url"; then
  echo "install.sh: download failed: $url" >&2
  exit 1
fi

# Optional checksum verification.
checksum_url="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
if curl -fsSL -o "$tmpdir/checksums.txt" "$checksum_url" 2>/dev/null; then
  expected=$(grep " $tarball\$" "$tmpdir/checksums.txt" | awk '{print $1}')
  if [ -n "$expected" ]; then
    actual=$(shasum -a 256 "$tmpdir/$tarball" 2>/dev/null | awk '{print $1}')
    [ -z "$actual" ] && actual=$(sha256sum "$tmpdir/$tarball" 2>/dev/null | awk '{print $1}')
    if [ -n "$actual" ] && [ "$expected" != "$actual" ]; then
      echo "install.sh: checksum mismatch for $tarball" >&2
      echo "  expected: $expected" >&2
      echo "  actual:   $actual" >&2
      exit 1
    fi
  fi
fi

mkdir -p "$tmpdir/extract"
tar xzf "$tmpdir/$tarball" -C "$tmpdir/extract"

# -- install -------------------------------------------------------------------
mkdir -p "$PREFIX/bin"
mv "$tmpdir/extract/agent-irc" "$PREFIX/bin/agent-irc"
chmod +x "$PREFIX/bin/agent-irc"

echo "agent-irc: installed $VERSION to $PREFIX/bin/agent-irc"

# Path-hint check.
case ":$PATH:" in
  *":$PREFIX/bin:"*) ;;
  *)
    echo "agent-irc: note: $PREFIX/bin is not in \$PATH"
    echo "  add to your shell rc:  export PATH=\"$PREFIX/bin:\$PATH\""
    ;;
esac
