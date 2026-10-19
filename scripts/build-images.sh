#!/usr/bin/env bash
# Build every container image, tagged ctf/<service>:<tag>.
#
# Usage:
#   ./scripts/build-images.sh            # builds :v1
#   ./scripts/build-images.sh v2         # builds :v2 (version baked into each binary)
#   ACCENT='#e0a83b' ./scripts/build-images.sh v2   # v2 frontend in mustard, for the rollout demo
set -euo pipefail
cd "$(dirname "$0")/.."     # run from the repo root no matter where it's called from

TAG="${1:-v1}"
ACCENT="${ACCENT:-#d0604a}"  # frontend accent colour (v1 default: brick red)

for svc in auth challenges submissions scoreboard; do
  # --platform linux/amd64: GKE's e2 nodes are x86; keeps builds identical everywhere
  # --build-arg APP_VERSION: shows up in the X-App-Version response header
  docker build --platform linux/amd64 \
    --build-arg APP_VERSION="$TAG" \
    -t "ctf/$svc:$TAG" "services/$svc"
done

# The frontend also takes the accent colour, so v1 and v2 look different on screen.
docker build --platform linux/amd64 \
  --build-arg APP_VERSION="$TAG" \
  --build-arg ACCENT="$ACCENT" \
  -t "ctf/frontend:$TAG" frontend

echo "Built ctf/{auth,challenges,submissions,scoreboard,frontend}:$TAG"
