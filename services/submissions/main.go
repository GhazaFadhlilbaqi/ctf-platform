// Command submissions is the flag-submission microservice.
//
// It owns the solves table. To handle a submission it talks to two other
// services over the cluster network:
//
//	submissions --(is this flag right?)--> challenges  POST /internal/challenges/:id/verify
//	submissions --(give them points)-----> scoreboard  POST /internal/scoreboard/award
//
// Routes:
//
//	POST /api/submissions        (player) {challenge_id, flag} -> 200 {correct, already_solved, points}
//	GET  /api/submissions/me     (player) -> 200 [{challenge_id, points, solved_at}, ...]
//	GET  /api/submissions/info   (admin)  -> 200 {service, version, pod}
package main

import (
	"context"
	"net/http"
	"time"
)

// No foreign keys to users/challenges: those tables belong to other
// services. Each microservice owns its own data.
// UNIQUE (user_id, challenge_id) makes "solve it twice" impossible.
const schema = `
CREATE TABLE IF NOT EXISTS solves (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT      NOT NULL,
    username     TEXT        NOT NULL,
    challenge_id BIGINT      NOT NULL,
    points       INTEGER     NOT NULL,
    solved_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, challenge_id)
);`

func main() {
	setupLogging("submissions")

	secret := []byte(mustGetenv("JWT_SECRET"))

	ctx := context.Background()
	db, err := connectDB(ctx, mustGetenv("DATABASE_URL"))
	if err != nil {
		fatal("database connection failed", "err", err)
	}
	defer db.Close()

	if err := migrate(ctx, db, schema); err != nil {
		fatal("migration failed", "err", err)
	}

	h := &handler{
		db: db,
		// Inside Kubernetes these resolve through cluster DNS: "challenges"
		// is the name of the challenges Service.
		challengesURL: getenv("CHALLENGES_URL", "http://challenges:8080"),
		scoreboardURL: getenv("SCOREBOARD_URL", "http://scoreboard:8080"),
		// Always set a timeout on outbound calls. Without one a slow
		// dependency ties up our goroutines forever.
		http: &http.Client{Timeout: 3 * time.Second},
	}

	r := newRouter()
	healthRoutes(r, db.Ping)

	// Admin-only status endpoint (System status on the admin page).
	r.GET("/api/submissions/info", requireAuth(secret), requireAdmin(), h.info)

	api := r.Group("/api/submissions", requireAuth(secret))
	{
		api.POST("", h.submit)
		api.GET("/me", h.mySolves)
	}

	runServer(r, getenv("PORT", "8080"))
}
