#!/usr/bin/env bash
# Builds every plugin under plugins/ into signed packages plus a repository index.
#
#   KEY=path/to/signing.key BASE_URL=https://host/path/ scripts/build-plugins.sh
#   ONLY="ffmpeg-thumbnail" scripts/build-plugins.sh   # just these plugins, no index
#
# Without KEY a development key is created in .dev/plugin-dev.{key,pub};
# trust it with EASYCLOUD_PLUGINS_TRUSTED_KEYS="$(cat .dev/plugin-dev.pub)".
set -euo pipefail
cd "$(dirname "$0")/.."

OUT=${OUT:-dist/plugins}
BASE_URL=${BASE_URL:-}
PLATFORMS=${PLATFORMS:-"linux/amd64 linux/arm64 darwin/arm64 darwin/amd64"}
mkdir -p "$OUT" .dev
go build -o .dev/ecplugin ./cmd/ecplugin

if [[ -z "${KEY:-}" ]]; then
  KEY=.dev/plugin-dev.key
  [[ -f "$KEY" ]] || .dev/ecplugin keygen -out .dev/plugin-dev
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if [[ -n "${ONLY:-}" ]]; then
  dirs=(); for id in $ONLY; do
    [[ -f "plugins/$id/manifest.json" ]] || { echo "no plugin $id" >&2; exit 1; }
    dirs+=("plugins/$id/")
  done
else
  dirs=(plugins/*/)
fi

for dir in "${dirs[@]}"; do
  id=$(basename "$dir")
  stage="$work/$id"
  mkdir -p "$stage"
  # Copy everything except Go sources; backend binaries are built below.
  (cd "$dir" && find . -type f ! -name '*.go' ! -name '*_test.go' -print0 | xargs -0 -I{} cp --parents {} "$stage" 2>/dev/null || \
    find . -type f ! -name '*.go' -exec sh -c 'mkdir -p "'"$stage"'/$(dirname "$1")" && cp "$1" "'"$stage"'/$1"' _ {} \;)
  if ls "$dir"/*.go >/dev/null 2>&1; then
    for plat in $PLATFORMS; do
      os=${plat%/*}; arch=${plat#*/}
      echo "  build $id $os/$arch"
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "$stage/bin/$os-$arch/$id" "./$dir"
    done
  fi
  .dev/ecplugin pack -dir "$stage" -key "$KEY" -out "$OUT/$(jq -r '.id + "-" + .version' "$dir/manifest.json").zip"
done

# The release pipeline publishes one plugin at a time and rebuilds the index
# from all releases, so a partial build writes no index.
if [[ -z "${ONLY:-}" ]]; then
  .dev/ecplugin index -base "$BASE_URL" "$OUT"/*.zip > "$OUT/index.json"
  echo "wrote $OUT/index.json"
fi
