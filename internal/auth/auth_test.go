package auth

import (
	"testing"
	"time"
)

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
