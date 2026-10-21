#!/usr/bin/env bash
# Generic checks of the helper: formatting, go vet on every target OS, tests,
# and a build for each release target in dist/.
set -euo pipefail

cd "$(dirname "$0")/.."

# The exact Go of the release builds (scripts/build-release.sh): Go downloads
# it if the local one is different.
export GOTOOLCHAIN=go1.27.2

unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
	echo "gofmt: these files need formatting:" >&2
	echo "$unformatted" >&2
	exit 1
fi

go vet ./...
GOOS=windows go vet ./...
GOOS=darwin go vet ./...

go test ./...

mkdir -p dist
for target in windows/amd64 darwin/arm64 darwin/amd64 linux/amd64; do
	goos="${target%/*}"
	goarch="${target#*/}"
	output="dist/leadin-helper_${goos}_${goarch}"
	if [[ "$goos" == windows ]]; then
		output+=".exe"
	fi
	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -o "$output" ./cmd/leadin-helper
	echo "built $output"
done
