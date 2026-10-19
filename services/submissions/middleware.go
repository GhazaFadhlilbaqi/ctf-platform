package main

// middleware.go is copied identically into every service. The auth service
// SIGNS tokens; the other services only VERIFY them with the same secret, so
// none of them need to call the auth service on every request.

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Claims is what we put inside every JWT. Struct tags (`json:"uid"`) control
// the field names in the encoded token. Embedding jwt.RegisteredClaims adds
// the standard fields (exp, iat, iss, sub) and their validation for free.
type Claims struct {
	UserID   int64  `json:"uid"`
	Username string `json:"username"`
	Role     string `json:"role"` // "player" or "admin"
	jwt.RegisteredClaims
}

const (
	tokenIssuer = "ctf-auth"
	claimsKey   = "claims" // key used to stash claims in the Gin context
)

// requireAuth rejects requests without a valid "Authorization: Bearer <jwt>"
// header. On success the parsed claims are available via currentUser(c).
func requireAuth(secret []byte) gin.HandlerFunc {
	// Build the parser once. Pinning the algorithm stops the classic
	// "alg: none" / algorithm-confusion attacks.
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
	)
	keyFunc := func(*jwt.Token) (any, error) { return secret, nil }

	return func(c *gin.Context) {
		raw, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		claims := &Claims{}
		if _, err := parser.ParseWithClaims(raw, claims, keyFunc); err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		c.Set(claimsKey, claims)
		c.Next() // run the actual handler
	}
}

// requireAdmin must come after requireAuth in the chain.
func requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if currentUser(c).Role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admins only"})
			return
		}
		c.Next()
	}
}

// currentUser returns the claims requireAuth stored. Only call it in handlers
// that sit behind requireAuth, otherwise MustGet panics.
func currentUser(c *gin.Context) *Claims {
	return c.MustGet(claimsKey).(*Claims)
}
