// Command auth is the authentication microservice of the CTF platform.
//
// It is the only service that owns the users table and ever sees a password.
// It hands out signed JWTs; every other service trusts those tokens.
//
// Routes (all JSON):
//
//	POST /api/auth/register  {username, password}  -> 201 {token, user}
//	POST /api/auth/login     {username, password}  -> 200 {token, user}
//	GET  /api/auth/me        (Bearer token)        -> 200 {user}
//	GET  /api/auth/info      (admin token)         -> 200 {service, version, pod}
//	GET  /healthz, /readyz                         -> Kubernetes probes
package main

import (
	"context"
	"os"
	"time"
)

// schema is applied on every startup. IF NOT EXISTS makes it safe to re-run.
const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL DEFAULT 'player' CHECK (role IN ('player', 'admin')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);`

func main() {
	setupLogging("auth")

	secret := mustGetenv("JWT_SECRET")
	if len(secret) < 32 {
		fatal("JWT_SECRET must be at least 32 characters")
	}

	ctx := context.Background()
	db, err := connectDB(ctx, mustGetenv("DATABASE_URL"))
	if err != nil {
		fatal("database connection failed", "err", err)
	}
	defer db.Close()

	if err := migrate(ctx, db, schema); err != nil {
		fatal("migration failed", "err", err)
	}

	h := newHandler(db, []byte(secret), 2*time.Hour)

	// Create the admin account on first boot if a password was provided.
	if pw := os.Getenv("ADMIN_PASSWORD"); pw != "" {
		if err := h.seedAdmin(ctx, getenv("ADMIN_USERNAME", "admin"), pw); err != nil {
			fatal("seeding admin failed", "err", err)
		}
	}

	r := newRouter()
	healthRoutes(r, db.Ping)

	// A route group shares a path prefix (and optionally middleware).
	api := r.Group("/api/auth")
	{
		// Admin-only: which pod/version answered (System status on the admin page).
		api.GET("/info", requireAuth(h.secret), requireAdmin(), h.info)
		api.POST("/register", h.register)
		api.POST("/login", h.login)
		// Middleware can be attached per route: requireAuth runs before h.me.
		api.GET("/me", requireAuth(h.secret), h.me)
	}

	runServer(r, getenv("PORT", "8080"))
}
