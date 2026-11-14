package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNVKEAgentPrivateRequestsNeverFollowRedirects(t *testing.T) {
	for _, kind := range []string{"configure", "request", "artifact", "stop"} {
		t.Run(kind, func(t *testing.T) {
			forwarded := 0
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded++
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = w.Write([]byte(`{"data":{}}`))
			}))
			defer destination.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
			}))
			defer source.Close()
			client := NewClient(source.URL, "PRIVATE_TOKEN", false)
			var err error
			switch kind {
			case "configure":
				_, err = client.ConfigureNVKEAgentAuthority(context.Background(), NVKEAgentConfigureInput{Credentials: &NVKEAgentCredentials{ClientKeyPEM: "PRIVATE_CLIENT_KEY"}})
			case "request":
				_, err = client.RequestNVKEAgentJob(context.Background(), NVKEAgentRequestInput{RequestText: "PRIVATE_NATIVE_INPUT"})
			case "artifact":
				_, err = client.NVKEAgentArtifact(context.Background(), NVKEAgentArtifactInput{Name: "PRIVATE_ARTIFACT"})
			case "stop":
				_, err = client.StopNVKEAgentJob(context.Background(), NVKEAgentJobReview{})
			}
			if forwarded != 0 || err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("private redirect followed=%d error=%v", forwarded, err)
			}
		})
	}
}
