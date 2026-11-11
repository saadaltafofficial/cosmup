#!/bin/sh
# Prints the CHANGELOG.md section for one version, for use as release notes.
# Usage: scripts/release-notes.sh 0.2.3
set -eu

version="${1#v}"
notes=$(awk -v v="$version" '
	/^## \[/ { if (found) exit; found = index($0, "## [" v "]") == 1; next }
	found
' CHANGELOG.md)

if [ -z "$(printf '%s' "$notes" | tr -d '[:space:]')" ]; then
	echo "CHANGELOG.md has no entries for $version" >&2
	exit 1
fi
printf '%s\n' "$notes"
