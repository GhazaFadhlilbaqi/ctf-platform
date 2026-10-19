#!/usr/bin/env bash
# Check every tool the project needs, install whatever is missing (via yay),
# and make sure Docker is running and usable without sudo.
#
#   bash check-setup.sh
#
# Safe to run again: anything already installed is skipped.
set -u

# gcloud's AUR package puts its tools in /opt/google-cloud-cli/bin, which is
# only added to PATH after you log in again, so look there too.
have() {
  command -v "$1" >/dev/null 2>&1 || [ -x "/opt/google-cloud-cli/bin/$1" ]
}

missing=()   # packages to install

# check <command> <package that provides it>
check() {
  if have "$1"; then
    printf '  ok       %s\n' "$1"
  else
    printf '  MISSING  %s  (package: %s)\n' "$1" "$2"
    missing+=("$2")
  fi
}

echo "== Checking tools"
check go                      go
check docker                  docker
check kubectl                 kubectl
check kind                    kind
check oha                     oha
check openssl                 openssl
check unzip                   unzip
check gcloud                  google-cloud-cli
check gke-gcloud-auth-plugin  google-cloud-cli-component-gke-gcloud-auth-plugin

# compose and buildx are docker sub-commands, not separate programs.
if have docker && docker compose version >/dev/null 2>&1; then
  echo "  ok       docker compose"
else
  echo "  MISSING  docker compose  (package: docker-compose)"
  missing+=(docker-compose)
fi
if have docker && docker buildx version >/dev/null 2>&1; then
  echo "  ok       docker buildx"
else
  echo "  MISSING  docker buildx  (package: docker-buildx)"
  missing+=(docker-buildx)
fi

# yay installs official-repo packages through pacman and builds AUR ones
# (the two google-cloud-cli packages); --needed skips anything already there.
if [ ${#missing[@]} -gt 0 ]; then
  echo
  echo "== Installing: ${missing[*]}"
  yay -S --needed "${missing[@]}" || { echo "Install failed, see the error above."; exit 1; }
else
  echo
  echo "== Nothing to install"
fi

echo
echo "== Docker service and permissions"
relogin=0
me="${USER:-$(id -un)}"   # current username
if systemctl is-active --quiet docker; then
  echo "  ok       docker service running"
else
  echo "  starting docker service (and enabling it on boot)"
  sudo systemctl enable --now docker
fi
if id -nG "$me" | grep -qw docker; then
  echo "  ok       $me is in the docker group"
else
  echo "  adding $me to the docker group"
  sudo usermod -aG docker "$me"
  relogin=1
fi

echo
echo "== Versions"
export PATH="$PATH:/opt/google-cloud-cli/bin"
go version
docker --version
docker compose version
kubectl version --client 2>/dev/null | head -n 1
kind version
oha --version 2>/dev/null | head -n 1
gcloud --version 2>/dev/null | head -n 1

echo
if [ "$relogin" -eq 1 ]; then
  echo "Done. Log out and back in (or reboot) so the docker group and gcloud PATH"
  echo "take effect, then run this script again to confirm everything is ok."
else
  echo "Done. Final check (should print 'Hello from Docker!' without sudo):"
  echo "  docker run --rm hello-world"
fi
