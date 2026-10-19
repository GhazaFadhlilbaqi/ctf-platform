package main

// Helpers for storing and checking flags. Done for you: use them in handlers.go.

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// hashFlag turns a flag into the hex string stored in challenges.flag_hash.
// We never store the plaintext flag, so a leaked database doesn't leak flags.
// Surrounding whitespace is trimmed so a stray space doesn't fail a correct flag.
func hashFlag(flag string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(flag)))
	return hex.EncodeToString(sum[:])
}

// flagMatches compares a submitted flag against a stored hash in constant
// time, so response timing can't leak how much of the hash matched.
func flagMatches(storedHash, submitted string) bool {
	return subtle.ConstantTimeCompare([]byte(storedHash), []byte(hashFlag(submitted))) == 1
}
