#!/usr/bin/env bash
# Create the "ctf" namespace and the "ctf-secrets" Secret in whichever cluster
# kubectl currently points at. Safe to re-run: an existing Secret is kept.
#
#   ADMIN_PASSWORD='something' ./scripts/create-secrets.sh   # choose the admin password
set -euo pipefail

# Create the namespace if it doesn't exist (dry-run + apply = idempotent "create")
kubectl create namespace ctf --dry-run=client -o yaml | kubectl apply -f -

if kubectl -n ctf get secret ctf-secrets >/dev/null 2>&1; then
  echo "Secret ctf-secrets already exists, keeping it."
else
  # Random values generated locally; they never get committed to git.
  #   JWT_SECRET        - HMAC key every service uses to sign/verify tokens
  #   POSTGRES_PASSWORD - hex, so it is safe inside the DATABASE_URL
  #   ADMIN_PASSWORD    - password of the "admin" account (only used on first boot)
  kubectl -n ctf create secret generic ctf-secrets \
    --from-literal=JWT_SECRET="$(openssl rand -hex 32)" \
    --from-literal=POSTGRES_PASSWORD="$(openssl rand -hex 16)" \
    --from-literal=ADMIN_PASSWORD="${ADMIN_PASSWORD:-$(openssl rand -hex 8)}"
  echo "Created Secret ctf-secrets."
fi

# Print the admin password so you can log in (base64 -d decodes the stored value)
echo -n "Admin login: admin / "
kubectl -n ctf get secret ctf-secrets -o jsonpath='{.data.ADMIN_PASSWORD}' | base64 -d
echo
