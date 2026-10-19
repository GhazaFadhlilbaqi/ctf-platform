#!/usr/bin/env bash
# Commands for the Week 13 demo, one sub-command per rubric item, so nothing
# has to be typed live. Works on kind and on GKE.
#
#   ./scripts/demo.sh status      # pods, nodes, HPAs, external address
#   ./scripts/demo.sh watch       # run in a 2nd terminal: one request every 0.2s, prints status codes
#   ./scripts/demo.sh scale       # manual scaling: challenges 2 -> 5 pods and back
#   ./scripts/demo.sh load        # autoscaling: hammer /api/auth/login, watch the HPA add pods
#   ./scripts/demo.sh kill-pod    # reliability: delete a pod, a new one replaces it
#   ./scripts/demo.sh drain       # reliability: take a whole node out of service
#   ./scripts/demo.sh uncordon    # put drained nodes back
#   ./scripts/demo.sh rollout     # rolling update of the frontend to v2 (accent turns mustard)
#   ./scripts/demo.sh rollback    # roll the frontend back to v1
#
# On GKE set the registry first:  export REGISTRY=australia-southeast1-docker.pkg.dev/<project>/ctf
set -euo pipefail
cd "$(dirname "$0")/.."

NS=ctf
REGISTRY="${REGISTRY:-ctf}"   # "ctf" = local kind images (ctf/frontend:v2)

# Public address of the app: the LoadBalancer IP on GKE, localhost:8081 on kind.
url() {
  local ip
  ip="$(kubectl -n $NS get svc frontend -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)"
  if [ -n "$ip" ]; then echo "http://$ip"; else echo "http://localhost:8081"; fi
}

case "${1:-help}" in

status)
  # Which node each pod runs on (-o wide), to show the spread across nodes.
  kubectl -n $NS get pods -o wide
  # Nodes in the cluster and their state (Ready / SchedulingDisabled).
  kubectl get nodes
  # Current vs target CPU and replica count for every autoscaler.
  kubectl -n $NS get hpa
  echo "App: $(url)"
  ;;

watch)
  # Fire a request every 0.2s at a public endpoint and print the HTTP code.
  # During scaling, pod kills, drains and rollouts this should stay all 200s.
  U="$(url)/api/scoreboard"
  while true; do
    curl -s -o /dev/null -w '%{http_code} ' --max-time 2 "$U" || echo -n 'ERR '
    sleep 0.2
  done
  ;;

scale)
  # The HPA owns the replica count, so raise its minimum instead of
  # `kubectl scale` (which the HPA would immediately undo).
  kubectl -n $NS patch hpa challenges -p '{"spec":{"minReplicas":5}}'
  # Watch the new pods appear for 25 seconds.
  timeout 25 kubectl -n $NS get pods -l app=challenges -w || true
  # Back to normal: minimum 2; the HPA scales in after its 60s window.
  kubectl -n $NS patch hpa challenges -p '{"spec":{"minReplicas":2}}'
  ;;

load)
  # Login runs bcrypt (deliberately CPU-heavy), so hammering it pushes the
  # auth pods' CPU over the 60% target and the HPA adds pods.
  # oha: `sudo pacman -S oha`. -z 90s = run 90 seconds, -c 40 = 40 concurrent clients.
  oha -z 90s -c 40 -m POST \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"wrong-password"}' \
    "$(url)/api/auth/login" &
  # Meanwhile watch the auth autoscaler react (TARGETS climbs, REPLICAS grows).
  timeout 95 kubectl -n $NS get hpa auth -w || true
  wait
  # Scale-in happens about a minute after the load stops (stabilization window).
  ;;

kill-pod)
  # Pick one challenges pod and delete it. The Deployment notices it has
  # fewer replicas than desired and immediately starts a replacement, while
  # the Service keeps routing to the surviving pod.
  POD="$(kubectl -n $NS get pods -l app=challenges -o jsonpath='{.items[0].metadata.name}')"
  kubectl -n $NS delete pod "$POD" --wait=false
  # Watch the replacement come up for 20 seconds.
  timeout 20 kubectl -n $NS get pods -l app=challenges -w || true
  ;;

drain)
  # Drain a worker that is NOT running Postgres (single replica; see README).
  PG_NODE="$(kubectl -n $NS get pod postgres-0 -o jsonpath='{.spec.nodeName}')"
  NODE="$(kubectl get nodes --no-headers -o custom-columns=NAME:.metadata.name \
          | grep -v control-plane | grep -vx "$PG_NODE" | head -n1)"
  echo "Draining $NODE (postgres is on $PG_NODE)"
  # cordon = no new pods; then evict existing ones. PodDisruptionBudgets make
  # sure at least one pod of every service stays up while this happens.
  #   --ignore-daemonsets      system per-node pods (logging etc.) stay put
  #   --delete-emptydir-data   allow evicting pods that use scratch volumes
  kubectl drain "$NODE" --ignore-daemonsets --delete-emptydir-data
  kubectl -n $NS get pods -o wide
  ;;

uncordon)
  # Allow scheduling on every node again after a drain.
  for n in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
    kubectl uncordon "$n"
  done
  ;;

rollout)
  # Requires a v2 frontend image:
  #   kind: ACCENT='#e0a83b' ./scripts/build-images.sh v2 && kind load docker-image ctf/frontend:v2 --name ctf
  #   GKE:  build as above, then docker tag + docker push to $REGISTRY/frontend:v2
  # Swap the image; Kubernetes replaces pods one at a time (maxSurge 1,
  # maxUnavailable 0), so the site never goes down.
  kubectl -n $NS set image deployment/frontend frontend="$REGISTRY/frontend:v2"
  # Record why this revision exists; shown by `rollout history`.
  kubectl -n $NS annotate deployment/frontend kubernetes.io/change-cause="v2: mustard accent" --overwrite
  # Follow the rollout until every pod runs v2.
  kubectl -n $NS rollout status deployment/frontend
  kubectl -n $NS rollout history deployment/frontend
  ;;

rollback)
  # List the recorded revisions, then go back to the previous one.
  kubectl -n $NS rollout history deployment/frontend
  kubectl -n $NS rollout undo deployment/frontend
  kubectl -n $NS rollout status deployment/frontend
  ;;

*)
  sed -n '2,16p' "$0"   # print the usage block at the top of this file
  ;;
esac
