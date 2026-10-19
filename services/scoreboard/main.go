// Command scoreboard is the live-leaderboard microservice.
//
// Scores live in a Redis sorted set: a structure that keeps members ordered by
// score, so "top 20 players" is one O(log n) command instead of a SQL
// GROUP BY over every solve. That's what you want during the traffic spike at
// the start of a CTF, when everyone refreshes the scoreboard.
//
// Routes:
//
//	GET  /api/scoreboard?limit=20               -> 200 [{rank, username, score}, ...]
//	POST /internal/scoreboard/award  {username, points} -> 204
//	GET  /api/scoreboard/info  (admin)          -> 200 {service, version, pod}
package main

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// scoreboardKey is the Redis key of the sorted set.
// member = username, score = total points.
const scoreboardKey = "scoreboard"

func main() {
	setupLogging("scoreboard")

	// Only used to check the admin token on /api/scoreboard/info.
	secret := []byte(mustGetenv("JWT_SECRET"))

	// The redis client keeps a connection pool internally; one client is
	// shared by all requests. It connects lazily, so no retry loop is needed:
	// /readyz simply reports "not ready" until Redis answers.
	rdb := redis.NewClient(&redis.Options{
		Addr:     getenv("REDIS_ADDR", "redis:6379"),
		Password: getenv("REDIS_PASSWORD", ""),
	})
	defer rdb.Close()

	h := &handler{rdb: rdb}

	r := newRouter()
	healthRoutes(r, func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	})

	// The scoreboard is public: no login needed to watch it.
	r.GET("/api/scoreboard", h.top)
	// Admin-only status endpoint (System status on the admin page).
	r.GET("/api/scoreboard/info", requireAuth(secret), requireAdmin(), h.info)

	// Internal only: called by the submissions service after a first solve.
	r.POST("/internal/scoreboard/award", h.award)

	runServer(r, getenv("PORT", "8080"))
}
