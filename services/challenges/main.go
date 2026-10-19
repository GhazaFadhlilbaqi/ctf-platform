// Command challenges is the challenge-catalogue microservice.
//
// It owns the challenges table and is the only service that knows the flags
// (stored as SHA-256 hashes). Other services ask it to check a flag through an
// internal endpoint that nginx never exposes to the internet.
//
// Routes:
//
//	GET    /api/challenges            (player)  -> 200 [challenge, ...]
//	GET    /api/challenges/:id        (player)  -> 200 challenge | 404
//	POST   /api/challenges            (admin)   {title, description, category, points, flag} -> 201 challenge
//	PUT    /api/challenges/:id        (admin)   same body, flag optional          -> 200 challenge | 404
//	DELETE /api/challenges/:id        (admin)   -> 204 | 404
//	POST   /internal/challenges/:id/verify      {flag} -> 200 {correct, points} | 404
//	GET    /api/challenges/info       (admin)   -> 200 {service, version, pod}
package main

import (
	"context"
)

const schema = `
CREATE TABLE IF NOT EXISTS challenges (
    id          BIGSERIAL PRIMARY KEY,
    title       TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    category    TEXT        NOT NULL,
    points      INTEGER     NOT NULL CHECK (points > 0),
    flag_hash   TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);`

func main() {
	setupLogging("challenges")

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

	h := &handler{db: db}

	r := newRouter()
	healthRoutes(r, db.Ping)

	// Admin-only status endpoint. Gin prefers this static route over "/:id".
	r.GET("/api/challenges/info", requireAuth(secret), requireAdmin(), h.info)

	// Public-facing API: every route needs a logged-in user.
	api := r.Group("/api/challenges", requireAuth(secret))
	{
		api.GET("", h.list)
		api.GET("/:id", h.get)

		// A nested group inherits requireAuth and adds requireAdmin on top.
		admin := api.Group("", requireAdmin())
		admin.POST("", h.create)
		admin.PUT("/:id", h.update)
		admin.DELETE("/:id", h.remove)
	}

	// Internal API: only reachable inside the cluster (nginx only forwards
	// /api/...), so no token is needed. The submissions service calls it.
	r.POST("/internal/challenges/:id/verify", h.verify)

	runServer(r, getenv("PORT", "8080"))
}
