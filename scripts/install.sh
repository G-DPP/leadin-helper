#!/bin/sh
# Installs the LeadIn helper from its GitHub release: downloads the binary
# for this computer and SHA256SUMS, checks the hash, runs `--install`.
#
#   curl -fsSL https://<LeadIn server>/api/v1/helper/install.sh | sh
#
# The release build writes its own version below (scripts/build-release.sh),
# so the script always installs the release it belongs to. A file fetched by
# curl has no quarantine mark: macOS Gatekeeper does not stop it.
# LEADIN_HELPER_BASE_URL replaces the release URL, for tests.
set -eu

VERSION="__VERSION__"
BASE_URL="${LEADIN_HELPER_BASE_URL:-https://github.com/G-DPP/leadin-helper/releases/download/v$VERSION}"

fail() {
	echo "ERROR: $*" >&2
	exit 1
}

case "$(uname -s)" in
Linux) goos=linux ;;
Darwin) goos=darwin ;;
*) fail "this script is for Linux and macOS; on Windows use install.ps1" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) goarch=amd64 ;;
arm64 | aarch64) goarch=arm64 ;;
*) fail "unsupported processor: $(uname -m)" ;;
esac
if [ "$goos" = linux ] && [ "$goarch" != amd64 ]; then
	fail "there is no Linux $goarch build of the helper yet"
fi

file="leadin-helper_${VERSION}_${goos}_${goarch}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

download() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -q "$1" -O "$2"
	else
		fail "curl or wget is needed"
	fi
}

echo "Downloading LeadIn helper $VERSION for $goos/$goarch…"
download "$BASE_URL/SHA256SUMS" "$tmp/SHA256SUMS" || fail "cannot download $BASE_URL/SHA256SUMS"
download "$BASE_URL/$file" "$tmp/$file" || fail "cannot download $BASE_URL/$file"

expected="$(awk -v name="$file" '$2 == name || $2 == "*" name { print $1 }' "$tmp/SHA256SUMS")"
[ -n "$expected" ] || fail "$file is not in SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$tmp/$file" | awk '{ print $1 }')"
else
	actual="$(shasum -a 256 "$tmp/$file" | awk '{ print $1 }')"
fi
[ "$expected" = "$actual" ] || fail "the hash of $file does not match SHA256SUMS: nothing was installed"
echo "Hash checked: $actual"

chmod +x "$tmp/$file"
"$tmp/$file" --install
