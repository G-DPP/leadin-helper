#!/usr/bin/env bash
# Builds the files of a release in dist/release/: one binary per target, a
# zip for the double click on Linux and macOS, and SHA256SUMS.
#
# The build is reproducible: the same commit gives the same SHA256SUMS on any
# machine, in any folder, at any time. scripts/verify-release.sh relies on it.
#
# Usage: scripts/build-release.sh X.Y.Z
set -euo pipefail

cd "$(dirname "$0")/.."

if [[ $# -ne 1 || ! "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo 'ERROR: version must be X.Y.Z (no "v", no pre-release)' >&2
	exit 2
fi
version="$1"

for tool in git go zip sha256sum; do
	if ! command -v "$tool" >/dev/null; then
		echo "ERROR: $tool is required but was not found" >&2
		exit 1
	fi
done

# A release must match one commit exactly; with local changes Go would also
# write vcs.modified=true into the binaries.
if [[ -n "$(git status --porcelain)" ]]; then
	echo "ERROR: the working tree has uncommitted changes; commit them first" >&2
	exit 1
fi

# Everything that could change the output is fixed here. GOTOOLCHAIN makes
# Go download this exact version if the local one is different.
export GOTOOLCHAIN=go1.27.2 CGO_ENABLED=0 TZ=UTC LC_ALL=C
SOURCE_DATE_EPOCH="$(git log -1 --format=%ct)"
export SOURCE_DATE_EPOCH
module="$(go list -m)"

out=dist/release
rm -rf "$out"
mkdir -p "$out"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

for target in linux/amd64 darwin/arm64 darwin/amd64 windows/amd64; do
	goos="${target%/*}"
	goarch="${target#*/}"
	name="leadin-helper_${version}_${goos}_${goarch}"
	binary="$out/$name"
	if [[ "$goos" == windows ]]; then
		binary+=".exe"
	fi
	# -trimpath drops the paths of this machine, -buildid= the identifier
	# that depends on the build cache. The linker signs darwin/arm64 ad hoc
	# by itself, also when building from Linux: there is no extra step.
	GOOS="$goos" GOARCH="$goarch" go build -trimpath \
		-ldflags "-s -w -buildid= -X ${module}/internal/version.Version=${version}" \
		-o "$binary" ./cmd/leadin-helper

	if [[ "$goos" != windows ]]; then
		# One file with a fixed name, so a double click never meets " (1)";
		# fixed date and no extra attributes, so the zip always has the same
		# hash.
		rm -rf "$tmp/zip"
		mkdir "$tmp/zip"
		cp "$binary" "$tmp/zip/leadin-helper"
		chmod 0755 "$tmp/zip/leadin-helper"
		touch -d "@${SOURCE_DATE_EPOCH}" "$tmp/zip/leadin-helper"
		(cd "$tmp/zip" && zip -X -D -q "$tmp/$name.zip" leadin-helper)
		mv "$tmp/$name.zip" "$out/$name.zip"
	fi
done

(
	cd "$out"
	sha256sum -- leadin-helper_* | sort -k2 >"$tmp/SHA256SUMS"
	mv "$tmp/SHA256SUMS" SHA256SUMS
	sha256sum -c --quiet SHA256SUMS
)

echo "Release $version ($(git rev-parse --short HEAD)) in $out:"
ls -l "$out"
echo
cat "$out/SHA256SUMS"
