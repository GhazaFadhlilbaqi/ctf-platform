#!/usr/bin/env bash
# Build, push and deploy (or redeploy) the platform on GKE.
#
#   PROJECT_ID=my-gcp-project ./scripts/gke-deploy.sh        # deploys :v1
#   PROJECT_ID=my-gcp-project ./scripts/gke-deploy.sh v1
set -euo pipefail
cd "$(dirname "$0")/.."

: "${PROJECT_ID:?set PROJECT_ID to your GCP project id}"
REGION="${REGION:-australia-southeast1}"
TAG="${1:-v1}"
REGISTRY="$REGION-docker.pkg.dev/$PROJECT_ID/ctf"

# Make sure kubectl is pointed at the GKE cluster, not kind, before changing anything.
case "$(kubectl config current-context)" in
  gke_*) ;;
  *) echo "kubectl context is not a GKE cluster. Run gcloud container clusters get-credentials first." >&2; exit 1 ;;
esac

# 1. Build all images locally.
./scripts/build-images.sh "$TAG"

# 2. Re-tag each image with its Artifact Registry name and push it.
for svc in auth challenges submissions scoreboard frontend; do
  docker tag "ctf/$svc:$TAG" "$REGISTRY/$svc:$TAG"
  docker push "$REGISTRY/$svc:$TAG"
done

# 3. Namespace + secrets (keeps existing ones).
./scripts/create-secrets.sh

# 4. Apply all manifests through the GKE overlay.
kubectl apply -k k8s/overlays/gke

# 5. Wait for every workload to be ready.
kubectl -n ctf rollout status statefulset/postgres --timeout=300s
for d in auth challenges submissions scoreboard frontend; do
  kubectl -n ctf rollout status "deployment/$d" --timeout=300s
done

# 6. Wait for Google Cloud to assign the load balancer a public IP (~1 minute).
echo "Waiting for the load balancer IP..."
for _ in $(seq 60); do
  IP="$(kubectl -n ctf get service frontend -o jsonpath='{.status.loadBalancer.ingress[0].ip}')"
  [ -n "$IP" ] && break
  sleep 5
done
echo "Live at http://${IP:-<pending, check: kubectl -n ctf get svc frontend>}"
