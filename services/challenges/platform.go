package main

// platform.go is boilerplate that every Postgres-backed service copies as-is:
// reading env config, connecting to the database with retries, running the
// schema migration safely, and an HTTP server that shuts down gracefully.
// You shouldn't need to change anything in here.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// version is replaced at build time by the Dockerfile:
//
//	go build -ldflags "-X main.version=v2"
//
// The admin-only /api/<service>/info endpoint reports it, which is how you
// prove during a rolling update which version each pod is running.
var version = "dev"

// migrationLockID is an arbitrary number every service agrees on. Holding a
// Postgres advisory lock on it means only one pod creates tables at a time,
// even when Kubernetes starts several replicas at the same moment.
const migrationLockID = 727

// setupLogging makes every log line JSON (GKE's Cloud Logging parses it) and
// tags it with the service name.
func setupLogging(service string) {
	h := slog.NewJSONHandler(os.Stdout, nil)
	slog.SetDefault(slog.New(h).With("service", service))
}

// getenv returns an environment variable, or fallback if it isn't set.
func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// mustGetenv returns an environment variable or stops the program. Better to
// crash at startup (Kubernetes shows the error) than run half-configured.
func mustGetenv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fatal("missing required environment variable", "key", key)
	}
	return v
}

// fatal logs an error and exits. Kubernetes will restart the container.
func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

// connectDB opens a connection pool and waits (up to ~60s) for Postgres to
// answer. On a fresh cluster the database pod often starts after the app pods.
func connectDB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	for attempt := 1; attempt <= 30; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return pool, nil
		}
		slog.Info("waiting for database", "attempt", attempt, "err", err)
		time.Sleep(2 * time.Second)
	}
	pool.Close()
	return nil, fmt.Errorf("database not reachable: %w", err)
}

// migrate runs the service's CREATE TABLE statements inside one transaction
// that holds the advisory lock, so concurrent replicas can't race each other.
func migrate(ctx context.Context, db *pgxpool.Pool, schema string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	// Rollback is a no-op once Commit has succeeded, so it's safe to defer.
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}
	if _, err := tx.Exec(ctx, schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return tx.Commit(ctx)
}

// newRouter builds a Gin engine with the middleware every service wants.
func newRouter() *gin.Engine {
	r := gin.New()
	// We sit behind nginx and a load balancer; we never use the client IP for
	// anything security-related, so trust no proxy headers.
	_ = r.SetTrustedProxies(nil)
	r.Use(
		gin.Recovery(), // turn panics into 500s instead of crashing the pod
		gin.LoggerWithConfig(gin.LoggerConfig{
			SkipPaths: []string{"/healthz", "/readyz"}, // probes would flood the logs
		}),
	)
	return r
}

// healthRoutes registers the two endpoints Kubernetes probes:
//   - /healthz (liveness): the process is alive. Failing => container restarted.
//   - /readyz (readiness): dependencies reachable. Failing => pod gets no traffic.
func healthRoutes(r *gin.Engine, ready func(context.Context) error) {
	r.GET("/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			c.String(http.StatusServiceUnavailable, "not ready: %v", err)
			return
		}
		c.String(http.StatusOK, "ready")
	})
}

// runServer serves HTTP until Kubernetes sends SIGTERM, then stops accepting
// new connections and lets in-flight requests finish. This is what makes
// rolling updates zero-downtime.
func runServer(handler http.Handler, port string) {
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("listening", "port", port, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("server failed", "err", err)
		}
	}()

	<-ctx.Done() // block here until a shutdown signal arrives
	slog.Info("shutting down, finishing in-flight requests")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
}
