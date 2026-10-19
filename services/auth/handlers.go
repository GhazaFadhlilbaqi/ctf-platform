package main

// handlers.go holds the HTTP handlers. This is the file to study before you
// write the handlers for the other services: they all follow this pattern.
//
//   1. Bind and validate the JSON body into a struct (ShouldBindJSON).
//   2. Talk to the database using the request's context.
//   3. Map errors to the right HTTP status code.
//   4. Reply with c.JSON(status, value).

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// handler bundles the dependencies the HTTP handlers need. Methods on it
// (func (h *handler) login(...)) can then reach h.db, h.secret, etc. This is
// Go's usual alternative to global variables.
type handler struct {
	db        *pgxpool.Pool
	secret    []byte
	ttl       time.Duration
	dummyHash []byte // see login() for why this exists
}

func newHandler(db *pgxpool.Pool, secret []byte, ttl time.Duration) *handler {
	// A real bcrypt hash of a random-ish string, used to burn the same CPU
	// time on logins for usernames that don't exist.
	dummy, _ := bcrypt.GenerateFromPassword([]byte("timing-equaliser"), bcrypt.DefaultCost)
	return &handler{db: db, secret: secret, ttl: ttl, dummyHash: dummy}
}

// user is what we send back to clients. It deliberately has no password field,
// so the hash can never leak into a response by accident.
type user struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// registerRequest uses Gin's `binding` tags (go-playground/validator) so
// ShouldBindJSON rejects bad input before our code even runs.
// bcrypt only looks at the first 72 bytes of a password, hence max=72.
type registerRequest struct {
	Username string `json:"username" binding:"required,min=3,max=32,alphanum"`
	Password string `json:"password" binding:"required,min=8,max=72"`
}

// loginRequest is looser: any wrong input should just be "wrong credentials".
type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// authResponse is returned by both register and login.
type authResponse struct {
	Token string `json:"token"`
	User  user   `json:"user"`
}

// POST /api/auth/register
func (h *handler) register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "username must be 3-32 letters/digits; password 8-72 characters",
		})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		// Happens if the password is over 72 *bytes* (e.g. lots of emoji).
		c.JSON(http.StatusBadRequest, gin.H{"error": "password too long"})
		return
	}

	// QueryRow + RETURNING gives us the new row back in one round trip.
	// $1, $2 are placeholders: pgx sends values separately from the SQL,
	// so SQL injection is impossible here.
	var u user
	err = h.db.QueryRow(c.Request.Context(),
		`INSERT INTO users (username, password_hash)
		 VALUES ($1, $2)
		 RETURNING id, username, role`,
		req.Username, string(hash),
	).Scan(&u.ID, &u.Username, &u.Role)

	// errors.As checks whether err (or anything it wraps) is a Postgres error.
	// 23505 is Postgres' code for "unique constraint violated".
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		c.JSON(http.StatusConflict, gin.H{"error": "username already taken"})
		return
	}
	if err != nil {
		h.serverError(c, "insert user", err)
		return
	}

	token, err := h.issueToken(u)
	if err != nil {
		h.serverError(c, "sign token", err)
		return
	}
	c.JSON(http.StatusCreated, authResponse{Token: token, User: u})
}

// POST /api/auth/login
func (h *handler) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password required"})
		return
	}

	var u user
	var hash string
	err := h.db.QueryRow(c.Request.Context(),
		`SELECT id, username, role, password_hash FROM users WHERE username = $1`,
		req.Username,
	).Scan(&u.ID, &u.Username, &u.Role, &hash)

	if errors.Is(err, pgx.ErrNoRows) {
		// Unknown user: still run bcrypt so the response takes as long as a
		// wrong password would. Otherwise an attacker could tell which
		// usernames exist just by timing the replies.
		_ = bcrypt.CompareHashAndPassword(h.dummyHash, []byte(req.Password))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}
	if err != nil {
		h.serverError(c, "query user", err)
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}

	token, err := h.issueToken(u)
	if err != nil {
		h.serverError(c, "sign token", err)
		return
	}
	c.JSON(http.StatusOK, authResponse{Token: token, User: u})
}

// GET /api/auth/me (behind requireAuth)
//
// There's no logout endpoint: JWTs are stateless, so logging out means the
// browser throws the token away. Tokens also expire after h.ttl.
func (h *handler) me(c *gin.Context) {
	claims := currentUser(c)
	c.JSON(http.StatusOK, gin.H{"user": user{
		ID:       claims.UserID,
		Username: claims.Username,
		Role:     claims.Role,
	}})
}

// GET /api/auth/info, used by the frontend footer and the demo.
func (h *handler) info(c *gin.Context) {
	pod, _ := os.Hostname()
	c.JSON(http.StatusOK, gin.H{"service": "auth", "version": version, "pod": pod})
}

// issueToken signs a JWT for u with HMAC-SHA256.
func (h *handler) issueToken(u user) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   u.ID,
		Username: u.Username,
		Role:     u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   strconv.FormatInt(u.ID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(h.ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(h.secret)
}

// seedAdmin creates the admin account if it doesn't exist yet. ON CONFLICT DO
// NOTHING makes it safe when several replicas start at once.
func (h *handler) seedAdmin(ctx context.Context, username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = h.db.Exec(ctx,
		`INSERT INTO users (username, password_hash, role)
		 VALUES ($1, $2, 'admin')
		 ON CONFLICT (username) DO NOTHING`,
		username, string(hash),
	)
	return err
}

// serverError logs the real error but only tells the client "internal error",
// so database details never leak out.
func (h *handler) serverError(c *gin.Context, what string, err error) {
	slog.Error(what, "err", err, "path", c.FullPath())
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}
