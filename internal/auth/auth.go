// Package auth handles browser device-flow login + token refresh.
//
// Spec ref: spec 10 §3 (auth) + spec 12 §2 (sessions).
//
// Device flow: CLI hits POST /api/cli/v1/auth/start to get a
// session_id + a login URL. CLI opens the URL in the user's
// browser; user authenticates against the platform's web UI;
// platform marks the session complete. CLI polls
// POST /api/cli/v1/auth/complete until it returns the issued
// access + refresh tokens.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Credentials holds the tokens returned by a successful login.
type Credentials struct {
	AccessToken  string    `yaml:"access_token"`
	RefreshToken string    `yaml:"refresh_token"`
	ExpiresAt    time.Time `yaml:"expires_at"`
}

// IsExpired returns true if the access token has expired or will
// expire within the given grace period.
func (c *Credentials) IsExpired(grace time.Duration) bool {
	return time.Now().Add(grace).After(c.ExpiresAt)
}

// LoginSession represents an in-progress browser device flow login.
type LoginSession struct {
	SessionID    string `json:"session_id"`
	LoginURL     string `json:"login_url"`
	PollInterval int    `json:"poll_interval_seconds"`
	ExpiresIn    int    `json:"expires_in_seconds"`
}

// ErrPending is returned by PollLogin while the user hasn't
// completed the flow yet. Caller waits the poll interval and
// tries again.
var ErrPending = errors.New("auth: login pending")

// ErrExpired is returned when the device-flow session expired
// before the user completed it.
var ErrExpired = errors.New("auth: session expired; run `astro auth login` again")

// StartLogin initiates the browser device flow.
func StartLogin(ctx context.Context, apiURL string) (*LoginSession, error) {
	url := strings.TrimSuffix(apiURL, "/") + "/api/cli/v1/auth/start"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth/start: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("auth/start returned %d: %s", resp.StatusCode, body)
	}

	var session LoginSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("decoding session: %w", err)
	}
	if session.PollInterval <= 0 {
		session.PollInterval = 2
	}
	if session.ExpiresIn <= 0 {
		session.ExpiresIn = 600 // 10 min default
	}
	return &session, nil
}

// PollLogin polls auth/complete; returns ErrPending when not yet
// authenticated, ErrExpired when the session has expired, or
// Credentials on success.
func PollLogin(ctx context.Context, apiURL, sessionID string) (*Credentials, error) {
	url := strings.TrimSuffix(apiURL, "/") + "/api/cli/v1/auth/complete"
	body, _ := json.Marshal(map[string]string{"session_id": sessionID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth/complete: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var creds Credentials
		if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
			return nil, fmt.Errorf("decoding credentials: %w", err)
		}
		return &creds, nil
	case http.StatusAccepted:
		return nil, ErrPending
	case http.StatusGone, http.StatusNotFound:
		return nil, ErrExpired
	default:
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("auth/complete returned %d: %s", resp.StatusCode, respBody)
	}
}

// PollLoginUntil polls repeatedly until success / expiry / context
// cancellation.
func PollLoginUntil(ctx context.Context, apiURL string, session *LoginSession) (*Credentials, error) {
	deadline := time.Now().Add(time.Duration(session.ExpiresIn) * time.Second)
	interval := time.Duration(session.PollInterval) * time.Second
	for {
		if time.Now().After(deadline) {
			return nil, ErrExpired
		}
		creds, err := PollLogin(ctx, apiURL, session.SessionID)
		if err == nil {
			return creds, nil
		}
		if !errors.Is(err, ErrPending) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// RefreshCredentials exchanges a refresh token for a new access token.
func RefreshCredentials(ctx context.Context, apiURL, refreshToken string) (*Credentials, error) {
	url := strings.TrimSuffix(apiURL, "/") + "/api/cli/v1/auth/refresh"
	body, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth/refresh: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusGone {
		return nil, ErrExpired
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("auth/refresh returned %d: %s", resp.StatusCode, respBody)
	}

	var creds Credentials
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return nil, fmt.Errorf("decoding refreshed credentials: %w", err)
	}
	return &creds, nil
}
