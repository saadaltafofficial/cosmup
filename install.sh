#!/bin/sh
# Installs cosmup from its GitHub releases, verifying the download's SHA256.
#
#   curl -sSfL https://raw.githubusercontent.com/saadaltafofficial/cosmup/main/install.sh | sh
#
# Options (environment variables):
#   COSMUP_VERSION   release to install, e.g. v0.2.3 (default: latest)
#   COSMUP_HOME      cosmup's folder (default: ~/.cosmup); cosmup goes in its bin/
set -eu

REPO="saadaltafofficial/cosmup"

fail() {
	echo "cosmup install: $*" >&2
	exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "unsupported OS $(uname -s); cosmup supports Linux and macOS" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "unsupported architecture $(uname -m); cosmup supports amd64 and arm64" ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	fail "sha256sum or shasum is required to verify the download"
fi

version="${COSMUP_VERSION:-}"
if [ -z "$version" ]; then
	version=$(curl -sSf "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1) ||
		fail "couldn't look up the latest release"
	[ -n "$version" ] || fail "couldn't find the latest release of $REPO"
fi
case "$version" in v*) ;; *) version="v$version" ;; esac

archive="cosmup_${version#v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading cosmup $version ($os/$arch)"
curl -sSfL -o "$tmp/$archive" "$base/$archive" || fail "download failed: $base/$archive"
curl -sSfL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "download failed: $base/checksums.txt"

expected=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || fail "checksums.txt has no entry for $archive"
actual=$(sha256 "$tmp/$archive")
[ "$expected" = "$actual" ] || fail "checksum mismatch for $archive (expected $expected, got $actual)"
echo "Verified sha256 $actual"

tar -xzf "$tmp/$archive" -C "$tmp" cosmup || fail "archive has no cosmup binary"

bindir="${COSMUP_HOME:-$HOME/.cosmup}/bin"
mkdir -p "$bindir"
mv "$tmp/cosmup" "$bindir/cosmup.tmp"
chmod 0755 "$bindir/cosmup.tmp"
mv "$bindir/cosmup.tmp" "$bindir/cosmup"
echo "Installed $("$bindir/cosmup" version) to $bindir/cosmup"

case ":$PATH:" in
*":$bindir:"*) ;;
*)
	echo
	echo "Add cosmup to your PATH (and to your shell's rc file):"
	echo "  export PATH=\"$bindir:\$PATH\""
	;;
esac
