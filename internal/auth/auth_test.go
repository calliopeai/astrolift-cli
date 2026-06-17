package auth

import (
	"encoding/json"
	"testing"
	"time"
)

// TestCredentialsJSONDecode guards the device-flow token capture: the server's
// /complete + /refresh responses are snake_case JSON, so Credentials must carry
// json tags or json.Decode silently drops every field (CamelCase fields don't
// match snake_case keys), leaving empty tokens + a zero ExpiresAt on disk.
func TestCredentialsJSONDecode(t *testing.T) {
	// Exact shape returned by POST /api/cli/v1/auth/complete.
	resp := `{"access_token":"alft_at_abc","refresh_token":"alft_rt_def","expires_at":"2026-06-18T00:00:00Z","token_type":"Bearer"}`
	var c Credentials
	if err := json.Unmarshal([]byte(resp), &c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.AccessToken != "alft_at_abc" {
		t.Errorf("access_token not captured: got %q", c.AccessToken)
	}
	if c.RefreshToken != "alft_rt_def" {
		t.Errorf("refresh_token not captured: got %q", c.RefreshToken)
	}
	if c.ExpiresAt.IsZero() {
		t.Error("expires_at not captured (zero value) — token would read as expired")
	}
}

func TestCredentialsIsExpired(t *testing.T) {
	c := &Credentials{
		AccessToken: "x",
		ExpiresAt:   time.Now().Add(5 * time.Minute),
	}
	if c.IsExpired(time.Minute) {
		t.Error("token with 5m left shouldn't be expired with 1m grace")
	}
	if !c.IsExpired(10 * time.Minute) {
		t.Error("token with 5m left should be expired with 10m grace")
	}

	stale := &Credentials{ExpiresAt: time.Now().Add(-time.Minute)}
	if !stale.IsExpired(0) {
		t.Error("expired token should report expired")
	}
}
