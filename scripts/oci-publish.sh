#!/usr/bin/env bash
set -euo pipefail

registry=${1:-${REGISTRY:-}}
image=${IMAGE:-whop2p}
tag=${TAG:-latest}
root=$(cd "$(dirname "$0")/.." && pwd)

[[ -n "$registry" ]] || {
  echo "usage: REGISTRY=harbor.example.com $0 [registry]" >&2
  exit 2
}
command -v container >/dev/null || {
  echo "Apple container CLI is required; see https://github.com/apple/container" >&2
  exit 1
}

make -C "$root" linux-arm64 >/dev/null
image_ref="$registry/$image:$tag"
container build -f "$root/deploy/orchard/Containerfile" -t "$image_ref" "$root"
container push "$image_ref"
echo "pushed $image_ref"
