package main

// handlers.go: flag submissions. One submission touches three services:
//
//  1. challenges  -> is the flag right, and how many points is it worth?
//  2. our own DB  -> record the solve (UNIQUE stops double solves)
//  3. scoreboard  -> add the points to the player's total

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type handler struct {
	db            *pgxpool.Pool
	challengesURL string
	scoreboardURL string
	http          *http.Client
}

type submitRequest struct {
	ChallengeID int64  `json:"challenge_id" binding:"required,gt=0"`
	Flag        string `json:"flag" binding:"required,max=200"`
}

// submitResponse is what the frontend reads.
type submitResponse struct {
	Correct       bool `json:"correct"`
	AlreadySolved bool `json:"already_solved"`
	Points        int  `json:"points"`
}

// solve is one row of GET /api/submissions/me.
type solve struct {
	ChallengeID int64     `json:"challenge_id"`
	Points      int       `json:"points"`
	SolvedAt    time.Time `json:"solved_at"`
}

// POST /api/submissions
func (h *handler) submit(c *gin.Context) {
	var req submitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "challenge_id and flag are required"})
		return
	}
	player := currentUser(c)
	ctx := c.Request.Context()

	// 1. Ask the challenges service.
	result, err := h.verifyFlag(ctx, req.ChallengeID, req.Flag)
	if errors.Is(err, errChallengeNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err != nil {
		slog.Error("verify flag", "err", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "challenges service unavailable"})
		return
	}
	if !result.Correct {
		c.JSON(http.StatusOK, submitResponse{Correct: false})
		return
	}

	// 2. Record the solve. ON CONFLICT DO NOTHING + RowsAffected tells us,
	// in one atomic statement, whether this is the player's first solve.
	tag, err := h.db.Exec(ctx,
		`INSERT INTO solves (user_id, username, challenge_id, points)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id, challenge_id) DO NOTHING`,
		player.UserID, player.Username, req.ChallengeID, result.Points,
	)
	if err != nil {
		h.serverError(c, "insert solve", err)
		return
	}
	if tag.RowsAffected() == 0 {
		// Already solved before: correct, but no points the second time.
		c.JSON(http.StatusOK, submitResponse{Correct: true, AlreadySolved: true, Points: result.Points})
		return
	}

	// 3. Award the points. If the scoreboard is down, undo step 2 so the
	// player can simply submit again later and nothing is half-recorded
	// (a "compensating action", the microservice alternative to a
	// cross-service transaction).
	if err := h.awardPoints(ctx, player.Username, result.Points); err != nil {
		slog.Error("award points", "err", err)
		if _, undoErr := h.db.Exec(ctx,
			`DELETE FROM solves WHERE user_id = $1 AND challenge_id = $2`,
			player.UserID, req.ChallengeID,
		); undoErr != nil {
			slog.Error("undo solve", "err", undoErr)
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "scoreboard unavailable, please submit again"})
		return
	}

	c.JSON(http.StatusOK, submitResponse{Correct: true, Points: result.Points})
}

// GET /api/submissions/me
func (h *handler) mySolves(c *gin.Context) {
	rows, err := h.db.Query(c.Request.Context(),
		`SELECT challenge_id, points, solved_at
		 FROM solves WHERE user_id = $1
		 ORDER BY solved_at DESC`,
		currentUser(c).UserID,
	)
	if err != nil {
		h.serverError(c, "list solves", err)
		return
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[solve])
	if err != nil {
		h.serverError(c, "scan solves", err)
		return
	}
	if list == nil {
		list = []solve{}
	}
	c.JSON(http.StatusOK, list)
}

// GET /api/submissions/info
func (h *handler) info(c *gin.Context) {
	pod, _ := os.Hostname()
	c.JSON(http.StatusOK, gin.H{"service": "submissions", "version": version, "pod": pod})
}

// serverError logs the real error but only tells the client "internal error".
func (h *handler) serverError(c *gin.Context, what string, err error) {
	slog.Error(what, "err", err, "path", c.FullPath())
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}
