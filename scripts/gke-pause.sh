#!/usr/bin/env bash
# Save credits between work sessions by scaling the node pool to 0 VMs.
# Persistent disks (Postgres/Redis data) are kept, so nothing is lost.
#
#   ./scripts/gke-pause.sh          # stop the VMs
#   ./scripts/gke-pause.sh resume   # bring 3 VMs back (pods reschedule in a few minutes)
#
# Resume well before your marking slot and check ./scripts/demo.sh status.
set -euo pipefail

ZONE="${ZONE:-australia-southeast1-a}"
CLUSTER="${CLUSTER:-ctf-cluster}"
NODES=0
[ "${1:-}" = "resume" ] && NODES=3

# Resize the default node pool to $NODES VMs (--quiet skips the confirmation prompt).
gcloud container clusters resize "$CLUSTER" \
  --zone "$ZONE" \
  --node-pool default-pool \
  --num-nodes "$NODES" \
  --quiet
