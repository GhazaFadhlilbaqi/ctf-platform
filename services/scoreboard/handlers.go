package main

// handlers.go: the live scoreboard, backed by a Redis sorted set.
// go-redis commands map 1:1 onto Redis commands (ZREVRANGE, ZINCRBY, ...).

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type handler struct {
	rdb *redis.Client
}

// entry is one scoreboard row as the frontend expects it.
type entry struct {
	Rank     int    `json:"rank"`
	Username string `json:"username"`
	Score    int    `json:"score"`
}

// GET /api/scoreboard?limit=20
func (h *handler) top(c *gin.Context) {
	// Read ?limit and clamp it to 1..100 so nobody can ask for a million rows.
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil {
		limit = 20
	}
	limit = max(1, min(limit, 100))

	// ZREVRANGE ... WITHSCORES: members ordered by score, highest first.
	// Ranges are inclusive, hence limit-1.
	zs, err := h.rdb.ZRevRangeWithScores(c.Request.Context(), scoreboardKey, 0, int64(limit-1)).Result()
	if err != nil {
		slog.Error("read scoreboard", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	board := make([]entry, 0, len(zs)) // length 0, so an empty board encodes as []
	for i, z := range zs {
		// z.Member is an `any`; a type assertion gets the string back out.
		name, _ := z.Member.(string)
		board = append(board, entry{Rank: i + 1, Username: name, Score: int(z.Score)})
	}
	c.JSON(http.StatusOK, board)
}

// awardRequest is the contract with the submissions service.
type awardRequest struct {
	Username string `json:"username" binding:"required,max=32"`
	Points   int    `json:"points" binding:"required,gt=0,lte=1000"`
}

// POST /internal/scoreboard/award (cluster-internal, called by submissions)
func (h *handler) award(c *gin.Context) {
	var req awardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and positive points required"})
		return
	}
	// ZINCRBY adds to the member's score, creating the member if it's new.
	// It's atomic, so two pods awarding points at once can't lose an update.
	if err := h.rdb.ZIncrBy(c.Request.Context(), scoreboardKey, float64(req.Points), req.Username).Err(); err != nil {
		slog.Error("award points", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.Status(http.StatusNoContent)
}

// GET /api/scoreboard/info
func (h *handler) info(c *gin.Context) {
	pod, _ := os.Hostname()
	c.JSON(http.StatusOK, gin.H{"service": "scoreboard", "version": version, "pod": pod})
}
