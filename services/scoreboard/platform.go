package main

// platform.go is the same boilerplate as in the other services, minus the
// Postgres helpers (the scoreboard only talks to Redis): env config, logging,
// router setup, health probes and graceful shutdown. Nothing to change here.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

// version is replaced at build time by the Dockerfile:
//
//	go build -ldflags "-X main.version=v2"
//
// The admin-only /api/<service>/info endpoint reports it, which is how you
// prove during a rolling update which version each pod is running.
var version = "dev"

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
