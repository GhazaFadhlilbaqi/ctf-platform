# CTF Platform: INFS3208 Individual Project (Type I)

A small Capture-the-Flag platform built as Go microservices and deployed on
Kubernetes (GKE). Players register, browse challenges, submit flags and
climb a live scoreboard; admins manage challenges.

```
                  Internet
                      │
        ┌─────────────▼──────────────┐
        │ Google Cloud Load Balancer │   (Service type LoadBalancer)
        └─────────────┬──────────────┘
                      │
        ┌─────────────▼──────────────┐
        │ frontend  (nginx, 2-4 pods)│   static UI + API gateway
        └──┬─────────┬─────────┬─────┴───────────┐
   /api/auth  /api/challenges  /api/submissions  /api/scoreboard
           │         │         │                 │
     ┌─────▼──┐ ┌────▼──────┐ ┌▼───────────┐ ┌───▼────────┐
     │  auth  │ │challenges │◄┤submissions ├►│ scoreboard │   Go + Gin, 2-6 pods each (HPA)
     └───┬────┘ └────┬──────┘ └─────┬──────┘ └───┬────────┘
         │  users    │ challenges   │ solves     │ sorted set
     ┌───▼───────────▼──────────────▼───┐   ┌────▼─────┐
     │ PostgreSQL 18 (StatefulSet + PV) │   │ Redis 8  │ (StatefulSet + PV)
     └──────────────────────────────────┘   └──────────┘
```

Each service owns its own table, verifies the same JWT locally, and only talks
to other services over the cluster network. `/internal/...` routes are never
forwarded by nginx, so they're unreachable from outside the cluster.

## Layout

```
services/
  auth/          register, login, JWT issuing, admin seeding; owns the users table
  challenges/    challenge CRUD + internal flag verification; owns the challenges table
  submissions/   flag submission, calls challenges + scoreboard; owns the solves table
  scoreboard/    live leaderboard in a Redis sorted set
  */platform.go     shared boilerplate (DB connect, migrations, graceful shutdown)
  */middleware.go   shared JWT verification (requireAuth / requireAdmin)
frontend/        nginx config + vanilla JS single-page app
k8s/base/        Deployments, Services, HPAs, PDBs, StatefulSets
k8s/overlays/    kind (NodePort) and gke (LoadBalancer + Artifact Registry images)
scripts/         every command used to build, deploy and demo, all commented
docker-compose.yml   local development
docs/            architecture diagram (draw.io source + PNG)
```

## Where to read the code

Each service has the same shape, so reading one makes the others easy:
`main.go` (routes and wiring), `handlers.go` (the HTTP handlers),
`platform.go` and `middleware.go` (shared boilerplate). Start with
`services/auth/handlers.go`, then `services/submissions/handlers.go`, which
shows service-to-service calls and what happens when one of them fails.

## 0. One-time setup (CachyOS)

```bash
# Go toolchain, Docker, Kubernetes tools, and a load generator for the HPA demo
sudo pacman -S go docker docker-compose docker-buildx kubectl kind oha
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"        # then log out and back in
yay -S google-cloud-cli google-cloud-cli-component-gke-gcloud-auth-plugin

# Download dependencies and create go.sum for each service (needed by the Dockerfiles)
for s in auth challenges submissions scoreboard; do (cd services/$s && go mod tidy); done
```

## 1. Develop locally with Docker Compose

```bash
docker compose up --build            # whole stack -> http://localhost:8080
docker compose up --build -d challenges   # rebuild one service after editing it
docker compose logs -f challenges    # follow one service's logs
docker compose down                  # stop (add -v to also wipe the database)
```

Local admin login: `admin` / `adminpass123`.

## 2. Test on a local Kubernetes cluster (kind)

```bash
./scripts/kind-up.sh                 # cluster + images + metrics-server + deploy
# -> http://localhost:8081 (admin password is printed by the script)
./scripts/demo.sh status
kind delete cluster --name ctf       # tear down
```

Load the 8 starter challenges (a fresh cluster has an empty database):
`./scripts/seed-challenges.sh http://localhost:8081 <admin-password>`.

After changing code: `./scripts/build-images.sh v1`, `kind load docker-image ctf/<svc>:v1 --name ctf`,
then `kubectl -n ctf rollout restart deployment/<svc>`.

## 3. Deploy to GKE

```bash
gcloud auth login
PROJECT_ID=<your-project> ./scripts/gke-setup.sh    # once: APIs, registry, 3-node cluster
PROJECT_ID=<your-project> ./scripts/gke-deploy.sh   # build, push, deploy, print public IP
./scripts/gke-pause.sh               # between sessions: 0 nodes, saves credits
./scripts/gke-pause.sh resume        # back to 3 nodes (do this well before marking)
```

To tear everything down after marking:
`gcloud container clusters delete ctf-cluster --zone australia-southeast1-a` and
`gcloud artifacts repositories delete ctf --location australia-southeast1`.

## 4. Demo (4 minutes), mapped to the marking criteria

Open a second terminal running `./scripts/demo.sh watch`: it prints an HTTP
status every 0.2s, so the tutor sees zero failed requests throughout. Log in
as admin and keep the Admin page open: its **System status** table shows which
pods are answering and which version they run (admins only).

| Criterion | Command | What to point at |
|---|---|---|
| Microservices / containers | `./scripts/demo.sh status` | 5 Deployments, 2 StatefulSets, pods spread over nodes |
| Load balancing | Admin → System status → **Send 20 requests** | requests spread over several pods per service |
| Scalability (manual) | `./scripts/demo.sh scale` | challenges goes 2 → 5 pods with no errors |
| Scalability (auto) | `./scripts/demo.sh load` | auth HPA CPU climbs, replicas increase |
| Reliability | `./scripts/demo.sh kill-pod`, `./scripts/demo.sh drain` | replacement pod appears; site stays up; then `uncordon` |
| Rolling update | `./scripts/demo.sh rollout` | accent turns mustard, System status shows frontend v2, watch stays 200 |
| Rollback | `./scripts/demo.sh rollback` | back to the brick red v1 accent |

The v2 frontend image has to exist first, see the comment in `demo.sh rollout`.

## API

| Method | Path | Auth | Body → Response |
|---|---|---|---|
| POST | `/api/auth/register` | – | `{username, password}` → `201 {token, user}` |
| POST | `/api/auth/login` | – | `{username, password}` → `200 {token, user}` |
| GET | `/api/auth/me` | player | → `{user}` |
| GET | `/api/challenges` | player | → `[{id, title, description, category, points}]` |
| GET | `/api/challenges/:id` | player | → challenge / 404 |
| POST | `/api/challenges` | admin | `{title, description, category, points, flag}` → 201 challenge |
| PUT | `/api/challenges/:id` | admin | same, empty flag keeps the old one → 200 / 404 |
| DELETE | `/api/challenges/:id` | admin | → 204 / 404 |
| POST | `/api/submissions` | player | `{challenge_id, flag}` → `{correct, already_solved, points}` |
| GET | `/api/submissions/me` | player | → `[{challenge_id, points, solved_at}]` |
| GET | `/api/scoreboard?limit=20` | – | → `[{rank, username, score}]` |
| POST | `/internal/challenges/:id/verify` | cluster only | `{flag}` → `{correct, points, title}` |
| POST | `/internal/scoreboard/award` | cluster only | `{username, points}` → 204 |
| GET | `/api/<service>/info` | admin | → `{service, version, pod}` |

Logout is client-side: JWTs are stateless and expire after 2 hours.

## Database design

Each microservice owns its own data; no service reads another service's tables.

| Store | Owner | Structure | Notes |
|---|---|---|---|
| PostgreSQL `users` | auth | `id` PK, `username` UNIQUE, `password_hash` (bcrypt), `role` (`player`/`admin`), `created_at` | Admin account seeded on first boot |
| PostgreSQL `challenges` | challenges | `id` PK, `title`, `description`, `category`, `points` (>0), `flag_hash` (SHA-256), `created_at` | Plaintext flags are never stored |
| PostgreSQL `solves` | submissions | `id` PK, `user_id`, `username`, `challenge_id`, `points`, `solved_at`, UNIQUE(`user_id`, `challenge_id`) | The UNIQUE constraint makes double-solving impossible |
| Redis sorted set `scoreboard` | scoreboard | member = username, score = total points | `ZINCRBY` to award, `ZREVRANGE` for the top N |

There are no foreign keys between tables, because each table belongs to a different
service (database-per-service pattern). The scoreboard is derived data: it can be
rebuilt from the `solves` table.

## Design notes (useful for the proposal and Q&A)

- **Why Go:** static binaries in distroless images; pods start in well under a
  second and use a few MB of RAM, so autoscaling reacts fast on small nodes.
- **Zero-downtime updates:** `maxUnavailable: 0`, readiness probes, a 5s
  `preStop` sleep, and graceful shutdown on SIGTERM in `platform.go`.
- **Reliability:** ≥2 replicas, spread across nodes, PodDisruptionBudgets, probes.
- **Known limitation:** Postgres and Redis run as single replicas. A pod restart
  or reschedule causes a short outage of those dependencies. Production would
  use Cloud SQL (HA) and Memorystore, at extra cost. Drain demos therefore pick
  a node not running Postgres.
- **Security:**
  - every API route is authorised server-side (JWT, admin role where needed);
    the frontend only decides what to *show*, never what is *allowed*
  - service-to-service `/internal/...` routes are never forwarded by nginx
  - pod names and versions are only visible to admins; no pod/version headers
    on normal responses, and nginx hides its own version
  - flags stored as SHA-256 hashes and compared in constant time; bcrypt
    passwords; JWT algorithm pinned
  - containers run non-root with all Linux capabilities dropped (Go services
    also have a read-only root filesystem)

## Troubleshooting

- Missing tools or Docker permission errors → `bash scripts/check-setup.sh`.
- `go.sum: no such file` during `docker build` → run the `go mod tidy` loop in step 0.
- HPA shows `<unknown>` on kind → metrics-server isn't ready yet; wait a minute.
- Pods `CreateContainerConfigError` → the `ctf-secrets` Secret is missing; run `./scripts/create-secrets.sh`.
- `ImagePullBackOff` on GKE → check the project id in `k8s/overlays/gke/kustomization.yaml` and that the image was pushed.
- Admin password changed in the Secret but login fails → the admin is only created on first boot; the old password still applies.

