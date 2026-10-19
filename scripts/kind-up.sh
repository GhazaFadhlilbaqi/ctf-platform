#!/usr/bin/env bash
# Run the whole platform on a local Kubernetes cluster (kind; nodes set in k8s/kind-config.yaml).
# Afterwards open http://localhost:8081
set -euo pipefail
cd "$(dirname "$0")/.."

# 0. Pre-flight checks. The app images run anywhere, but kind runs Kubernetes
#    itself inside containers that share THIS machine's kernel, so the host
#    must be healthy. Each check below is a problem we actually hit.
docker info >/dev/null 2>&1 || { echo "Docker isn't running or you lack permission (are you in the docker group?)." >&2; exit 1; }
if [ "$(uname -s)" = "Linux" ]; then
  # After a kernel update the running kernel's modules may be gone until reboot;
  # kube-proxy then can't load netfilter modules and cluster networking breaks.
  if [ ! -d "/lib/modules/$(uname -r)" ]; then
    echo "Kernel modules for the running kernel ($(uname -r)) are missing - a kernel update is waiting for a reboot. Reboot, then rerun." >&2
    exit 1
  fi
  # Every node and pod watches files; desktop defaults are too low for a cluster.
  if [ "$(cat /proc/sys/fs/inotify/max_user_instances)" -lt 512 ]; then
    echo "Warning: fs.inotify.max_user_instances is below 512; kind pods may crash with 'too many open files'." >&2
    echo "  Fix: sudo sysctl fs.inotify.max_user_instances=512 fs.inotify.max_user_watches=524288" >&2
  fi
fi

# 1. Create the cluster from k8s/kind-config.yaml (skipped if "ctf" already exists).
kind get clusters | grep -qx ctf || kind create cluster --config k8s/kind-config.yaml

# 2. Build all images as ctf/<service>:v1
./scripts/build-images.sh v1

# 3. Copy the images into the kind nodes. kind runs its own container runtime
#    and can't see images in your local Docker otherwise.
for svc in auth challenges submissions scoreboard frontend; do
  kind load docker-image "ctf/$svc:v1" --name ctf
done

# 4. Install metrics-server, which the HorizontalPodAutoscalers read CPU usage from.
#    (GKE ships with it; kind doesn't.)
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.9.0/components.yaml
#    kind's kubelets use self-signed certificates, so tell metrics-server to accept them.
#    (Replaces the container args, so running this twice is harmless.)
kubectl -n kube-system patch deployment metrics-server --type=json -p='[
  {"op":"replace","path":"/spec/template/spec/containers/0/args","value":[
    "--cert-dir=/tmp","--secure-port=10250",
    "--kubelet-preferred-address-types=InternalIP,ExternalIP,Hostname",
    "--kubelet-use-node-status-port","--metric-resolution=15s",
    "--kubelet-insecure-tls"]}]'

# 5. Namespace + random secrets (prints the admin password).
./scripts/create-secrets.sh

# 6. Deploy everything through the kind overlay (kubectl has kustomize built in).
kubectl apply -k k8s/overlays/kind

# 7. Wait until every Deployment has finished rolling out.
kubectl -n ctf rollout status statefulset/postgres --timeout=180s
for d in auth challenges submissions scoreboard frontend; do
  kubectl -n ctf rollout status "deployment/$d" --timeout=180s
done

echo "Up. Open http://localhost:8081"
