#!/usr/bin/env bash
# Fails when a plugin changed after its current version was released, i.e. the
# change would ship without a version bump. Needs tags (fetch-depth: 0).
#
#   plugins/<id>/manifest.json  "version": "1.0.1"  <->  tag plugins/<id>/v1.0.1
set -euo pipefail
cd "$(dirname "$0")/.."
fail=0
for m in plugins/*/manifest.json; do
  dir=$(dirname "$m"); id=$(basename "$dir")
  ver=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$m" | head -1)
  tag="plugins/$id/v$ver"
  if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
    if ! git diff --quiet "$tag" HEAD -- "$dir"; then
      echo "::error file=$m::$id changed since $tag was released; bump \"version\" in $m"
      fail=1
    fi
  fi
done
exit $fail
