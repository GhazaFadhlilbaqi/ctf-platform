#!/usr/bin/env bash
# One-time Google Cloud setup: APIs, Artifact Registry, and the GKE cluster.
#
#   PROJECT_ID=my-gcp-project ./scripts/gke-setup.sh
#
# Takes ~5-8 minutes (cluster creation is the slow part).
set -euo pipefail
cd "$(dirname "$0")/.."

: "${PROJECT_ID:?set PROJECT_ID to your GCP project id}"
REGION="${REGION:-australia-southeast1}"     # Sydney, the closest region to Brisbane
ZONE="${ZONE:-australia-southeast1-a}"
CLUSTER="${CLUSTER:-ctf-cluster}"

# Point gcloud at the project for all following commands.
gcloud config set project "$PROJECT_ID"

# Turn on the two APIs we use: GKE and Artifact Registry (private image registry).
gcloud services enable container.googleapis.com artifactregistry.googleapis.com

# Create a Docker image repository named "ctf" in the region (skip if it exists).
gcloud artifacts repositories describe ctf --location "$REGION" >/dev/null 2>&1 ||
  gcloud artifacts repositories create ctf \
    --repository-format=docker \
    --location "$REGION" \
    --description "CTF platform images"

# Let the local `docker push` authenticate to Artifact Registry with your gcloud login.
gcloud auth configure-docker "$REGION-docker.pkg.dev" --quiet

# Allow the nodes' default service account to pull images from the repository.
# (Newer projects don't grant this automatically.)
PROJECT_NUMBER="$(gcloud projects describe "$PROJECT_ID" --format='value(projectNumber)')"
gcloud artifacts repositories add-iam-policy-binding ctf \
  --location "$REGION" \
  --member "serviceAccount:${PROJECT_NUMBER}-compute@developer.gserviceaccount.com" \
  --role roles/artifactregistry.reader >/dev/null

# Create a zonal GKE Standard cluster:
#   --num-nodes 3              three VMs, so pods spread out and a node can be drained
#   --machine-type e2-medium   2 shared vCPUs / 4 GB each, cheapest size that runs this well
#   --disk-type/--disk-size    30 GB standard disks instead of the 100 GB balanced default (cheaper)
#   --release-channel regular  GKE keeps the Kubernetes version patched for us
gcloud container clusters describe "$CLUSTER" --zone "$ZONE" >/dev/null 2>&1 ||
  gcloud container clusters create "$CLUSTER" \
    --zone "$ZONE" \
    --num-nodes 3 \
    --machine-type e2-medium \
    --disk-type pd-standard \
    --disk-size 30 \
    --release-channel regular

# Write cluster credentials into ~/.kube/config so kubectl talks to GKE.
# (Needs the gke-gcloud-auth-plugin installed.)
gcloud container clusters get-credentials "$CLUSTER" --zone "$ZONE"

# Put the project id into the GKE overlay's image names (one-time edit).
sed -i "s/YOUR_PROJECT_ID/$PROJECT_ID/g" k8s/overlays/gke/kustomization.yaml

echo "GKE ready. Next: PROJECT_ID=$PROJECT_ID ./scripts/gke-deploy.sh"
