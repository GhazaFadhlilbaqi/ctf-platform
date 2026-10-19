package main

// handlers.go: the challenge catalogue. Same pattern as the auth service:
// bind/validate input, query Postgres with the request's context, map errors
// to HTTP status codes, reply with c.JSON.

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type handler struct {
	db *pgxpool.Pool
}

// challenge is the JSON shape the frontend expects. There is deliberately no
// flag field, so a flag (or its hash) can never be sent to a player.
type challenge struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Points      int    `json:"points"`
}

// challengeColumns is the SELECT/RETURNING list matching the struct above,
// in the same order, so pgx.RowToStructByPos can fill it.
const challengeColumns = `id, title, description, category, points`

// challengeRequest is the body of create (POST) and update (PUT).
// Flag is checked by hand: required on create, optional on update.
type challengeRequest struct {
	Title       string `json:"title" binding:"required,max=100"`
	Description string `json:"description" binding:"max=5000"`
	Category    string `json:"category" binding:"required,max=30"`
	Points      int    `json:"points" binding:"required,min=1,max=1000"`
	Flag        string `json:"flag" binding:"max=200"`
}

// GET /api/challenges
func (h *handler) list(c *gin.Context) {
	rows, err := h.db.Query(c.Request.Context(),
		`SELECT `+challengeColumns+` FROM challenges ORDER BY category, points, id`)
	if err != nil {
		h.serverError(c, "list challenges", err)
		return
	}
	// CollectRows loops over the rows, scans each one into a challenge by
	// column position, closes the rows and returns any error.
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[challenge])
	if err != nil {
		h.serverError(c, "scan challenges", err)
		return
	}
	if list == nil {
		list = []challenge{} // encode an empty table as [] instead of null
	}
	c.JSON(http.StatusOK, list)
}

// GET /api/challenges/:id
func (h *handler) get(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	rows, err := h.db.Query(c.Request.Context(),
		`SELECT `+challengeColumns+` FROM challenges WHERE id = $1`, id)
	if err != nil {
		h.serverError(c, "get challenge", err)
		return
	}
	// CollectExactlyOneRow returns pgx.ErrNoRows when nothing matched.
	ch, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[challenge])
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err != nil {
		h.serverError(c, "scan challenge", err)
		return
	}
	c.JSON(http.StatusOK, ch)
}

// POST /api/challenges (admin)
func (h *handler) create(c *gin.Context) {
	req, ok := bindChallenge(c)
	if !ok {
		return
	}
	if req.Flag == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "flag is required"})
		return
	}

	// Store only the hash of the flag (see flags.go).
	rows, err := h.db.Query(c.Request.Context(),
		`INSERT INTO challenges (title, description, category, points, flag_hash)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+challengeColumns,
		req.Title, req.Description, req.Category, req.Points, hashFlag(req.Flag),
	)
	if err != nil {
		h.serverError(c, "insert challenge", err)
		return
	}
	ch, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[challenge])
	if err != nil {
		h.serverError(c, "insert challenge", err)
		return
	}
	c.JSON(http.StatusCreated, ch)
}

// PUT /api/challenges/:id (admin). An empty flag keeps the current one.
func (h *handler) update(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	req, ok := bindChallenge(c)
	if !ok {
		return
	}

	// NULLIF($5, '') turns an empty string into NULL, and COALESCE then
	// falls back to the existing flag_hash. So "" means "keep the old flag".
	newHash := ""
	if req.Flag != "" {
		newHash = hashFlag(req.Flag)
	}
	rows, err := h.db.Query(c.Request.Context(),
		`UPDATE challenges
		 SET title = $1, description = $2, category = $3, points = $4,
		     flag_hash = COALESCE(NULLIF($5, ''), flag_hash)
		 WHERE id = $6
		 RETURNING `+challengeColumns,
		req.Title, req.Description, req.Category, req.Points, newHash, id,
	)
	if err != nil {
		h.serverError(c, "update challenge", err)
		return
	}
	ch, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[challenge])
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err != nil {
		h.serverError(c, "update challenge", err)
		return
	}
	c.JSON(http.StatusOK, ch)
}

// DELETE /api/challenges/:id (admin)
func (h *handler) remove(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	// Exec is for statements that return no rows. The CommandTag it returns
	// says how many rows were affected: 0 means the id didn't exist.
	tag, err := h.db.Exec(c.Request.Context(), `DELETE FROM challenges WHERE id = $1`, id)
	if err != nil {
		h.serverError(c, "delete challenge", err)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	c.Status(http.StatusNoContent)
}

// verifyRequest / verifyResponse are the contract with the submissions service.
type verifyRequest struct {
	Flag string `json:"flag" binding:"required,max=200"`
}

type verifyResponse struct {
	Correct bool   `json:"correct"`
	Points  int    `json:"points"`
	Title   string `json:"title"`
}

// POST /internal/challenges/:id/verify (cluster-internal, called by submissions)
// A wrong flag is still 200; "correct": false is the answer.
func (h *handler) verify(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req verifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "flag is required"})
		return
	}

	var storedHash string
	var res verifyResponse
	err := h.db.QueryRow(c.Request.Context(),
		`SELECT flag_hash, points, title FROM challenges WHERE id = $1`, id,
	).Scan(&storedHash, &res.Points, &res.Title)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err != nil {
		h.serverError(c, "verify flag", err)
		return
	}

	res.Correct = flagMatches(storedHash, req.Flag)
	c.JSON(http.StatusOK, res)
}

// GET /api/challenges/info
func (h *handler) info(c *gin.Context) {
	pod, _ := os.Hostname()
	c.JSON(http.StatusOK, gin.H{"service": "challenges", "version": version, "pod": pod})
}

// ---------- helpers ----------

// idParam parses the :id path parameter. On failure it has already replied
// 400, so the caller just returns. (Two return values, "value, ok", is a
// common Go idiom.)
func idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge id"})
		return 0, false
	}
	return id, true
}

// bindChallenge validates a create/update body and normalises it.
func bindChallenge(c *gin.Context) (challengeRequest, bool) {
	var req challengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "title and category are required; points must be 1-1000",
		})
		return req, false
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Category = strings.ToLower(strings.TrimSpace(req.Category))
	req.Flag = strings.TrimSpace(req.Flag)
	if req.Title == "" || req.Category == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title and category can't be blank"})
		return req, false
	}
	return req, true
}

// serverError logs the real error but only tells the client "internal error".
func (h *handler) serverError(c *gin.Context, what string, err error) {
	slog.Error(what, "err", err, "path", c.FullPath())
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}
