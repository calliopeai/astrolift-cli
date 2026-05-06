package auth

import (
	"fmt"
	"time"
)

// Credentials holds the tokens returned by a successful login.
type Credentials struct {
	AccessToken  string    `yaml:"access_token"`
	RefreshToken string    `yaml:"refresh_token"`
	ExpiresAt    time.Time `yaml:"expires_at"`
}

// IsExpired returns true if the access token has expired or will expire
// within the given grace period.
func (c *Credentials) IsExpired(grace time.Duration) bool {
	return time.Now().Add(grace).After(c.ExpiresAt)
}

// LoginSession represents an in-progress browser device flow login.
type LoginSession struct {
	SessionID string `json:"session_id"`
	LoginURL  string `json:"login_url"`
}

// StartLogin initiates the browser device flow by calling the platform's
// auth/start endpoint. Returns the session for polling.
func StartLogin(apiURL string) (*LoginSession, error) {
	_ = apiURL
	return nil, fmt.Errorf("not implemented")
}

// PollLogin polls the auth/complete endpoint until the user completes the
// browser login or the session times out.
func PollLogin(apiURL string, sessionID string) (*Credentials, error) {
	_ = apiURL
	_ = sessionID
	return nil, fmt.Errorf("not implemented")
}

// RefreshCredentials exchanges a refresh token for a new access token.
func RefreshCredentials(apiURL string, refreshToken string) (*Credentials, error) {
	_ = apiURL
	_ = refreshToken
	return nil, fmt.Errorf("not implemented")
}
