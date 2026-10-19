package main

// clients.go: HTTP calls to the other microservices. Inside Kubernetes,
// "challenges" and "scoreboard" resolve through cluster DNS to their Services,
// which load-balance across the pods.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// errChallengeNotFound lets the handler tell "no such challenge" (404) apart
// from "the challenges service is broken" (502). Compare with errors.Is.
var errChallengeNotFound = errors.New("challenge not found")

// verifyResult mirrors verifyResponse in the challenges service.
type verifyResult struct {
	Correct bool   `json:"correct"`
	Points  int    `json:"points"`
	Title   string `json:"title"`
}

// verifyFlag asks the challenges service whether flag is right.
func (h *handler) verifyFlag(ctx context.Context, challengeID int64, flag string) (verifyResult, error) {
	url := fmt.Sprintf("%s/internal/challenges/%d/verify", h.challengesURL, challengeID)

	resp, err := h.postJSON(ctx, url, map[string]string{"flag": flag})
	if err != nil {
		return verifyResult{}, err
	}
	// Always close the body, otherwise the connection can't be reused.
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var result verifyResult
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return verifyResult{}, fmt.Errorf("decode verify response: %w", err)
		}
		return result, nil
	case http.StatusNotFound:
		return verifyResult{}, errChallengeNotFound
	default:
		return verifyResult{}, fmt.Errorf("challenges service returned %d", resp.StatusCode)
	}
}

// awardPoints tells the scoreboard service to add points to username.
func (h *handler) awardPoints(ctx context.Context, username string, points int) error {
	resp, err := h.postJSON(ctx, h.scoreboardURL+"/internal/scoreboard/award", map[string]any{
		"username": username,
		"points":   points,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("scoreboard service returned %d", resp.StatusCode)
	}
	return nil
}

// postJSON sends body as JSON and returns the response. The caller must close
// resp.Body. ctx is the incoming request's context, so if the player's request
// is cancelled, this outbound call is cancelled too.
func (h *handler) postJSON(ctx context.Context, url string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return h.http.Do(req) // h.http has a 3s timeout (see main.go)
}
