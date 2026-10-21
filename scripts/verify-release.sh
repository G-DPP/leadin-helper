#!/usr/bin/env bash
# Checks that the files of a release come from this code: rebuilds the tag in
# a temporary folder and compares every hash with the SHA256SUMS of the
# release. Anyone can run it; it needs git, bash, zip and any Go (the exact
# version is downloaded by scripts/build-release.sh).
#
# Usage: scripts/verify-release.sh X.Y.Z <SHA256SUMS file or https:// URL>
set -euo pipefail

cd "$(dirname "$0")/.."

if [[ $# -ne 2 ]]; then
	echo "usage: $0 X.Y.Z <SHA256SUMS file or https:// URL>" >&2
	exit 2
fi
version="$1"
sums="$2"

tmp="$(mktemp -d)"
worktree="$tmp/src"
cleanup() {
	git worktree remove --force "$worktree" 2>/dev/null || true
	rm -rf "$tmp"
}
trap cleanup EXIT

if [[ "$sums" == https://* ]]; then
	curl -fsSL "$sums" -o "$tmp/expected"
else
	cp "$sums" "$tmp/expected"
fi

if git rev-parse -q --verify "refs/tags/v$version" >/dev/null; then
	commit="v$version"
else
	echo "WARNING: tag v$version not found, verifying HEAD" >&2
	commit=HEAD
fi

git worktree add -q --detach "$worktree" "$commit"
"$worktree/scripts/build-release.sh" "$version" >/dev/null
actual="$worktree/dist/release/SHA256SUMS"

status=0
while read -r hash file; do
	got="$(awk -v f="$file" '$2 == f { print $1 }' "$actual")"
	if [[ -z "$got" ]]; then
		echo "MISSING   $file"
		status=1
	elif [[ "$got" == "$hash" ]]; then
		echo "OK        $file"
	else
		echo "MISMATCH  $file (expected $hash, built $got)"
		status=1
	fi
done <"$tmp/expected"
while read -r _ file; do
	if ! awk -v f="$file" '$2 == f { found = 1 } END { exit !found }' "$tmp/expected"; then
		echo "EXTRA     $file"
		status=1
	fi
done <"$actual"

exit "$status"
