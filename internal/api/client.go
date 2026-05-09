package api

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

// Client handles communication with the Astrolift platform API.
// It supports both GraphQL and REST endpoints.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
	debug      bool
}

// NewClient creates a Client configured for the given API base URL.
func NewClient(baseURL, token string, debug bool) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		debug:   debug,
	}
}

// SetTimeout overrides the default 30s timeout. Streaming endpoints
// (log tail, exec) need much longer or no timeout.
func (c *Client) SetTimeout(d time.Duration) {
	c.httpClient.Timeout = d
}

// GraphQL sends a GraphQL query to the platform API and decodes the
// data field into the provided target. The target is the inner shape
// (i.e. `{"app": {...}}`), not the full envelope.
func (c *Client) GraphQL(ctx context.Context, query string, variables map[string]interface{}, target interface{}) error {
	body := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshalling graphql body: %w", err)
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/graphql/", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("graphql request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading graphql response: %w", err)
	}

	if c.debug {
		fmt.Fprintf(io.Discard, "graphql response status=%d body=%s\n", resp.StatusCode, respBytes)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("graphql server error %d: %s", resp.StatusCode, respBytes)
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBytes, &envelope); err != nil {
		return fmt.Errorf("parsing graphql envelope: %w", err)
	}
	if len(envelope.Errors) > 0 {
		msgs := make([]string, len(envelope.Errors))
		for i, e := range envelope.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("graphql errors: %s", strings.Join(msgs, "; "))
	}
	if target != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			return fmt.Errorf("decoding graphql data: %w", err)
		}
	}
	return nil
}

// ErrUnauthorized signals the token is missing or invalid; callers
// should prompt the user to re-login.
var ErrUnauthorized = errors.New("unauthorized: run `astro auth login`")

// Get performs an authenticated GET request against the REST API and
// decodes the JSON response body into target.
func (c *Client) Get(ctx context.Context, path string, target interface{}) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("get %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s returned %d: %s", path, resp.StatusCode, body)
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

// Post performs an authenticated POST against the REST API.
func (c *Client) Post(ctx context.Context, path string, body interface{}, target interface{}) error {
	var rdr io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshalling post body: %w", err)
		}
		rdr = bytes.NewReader(encoded)
	}
	req, err := c.newRequest(ctx, http.MethodPost, path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s returned %d: %s", path, resp.StatusCode, respBody)
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

// Stream issues a streaming GET (e.g. SSE for logs). Caller reads
// from the returned ReadCloser and is responsible for closing it.
func (c *Client) Stream(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	// Streaming endpoints don't get the default 30s timeout.
	streamingClient := &http.Client{}
	resp, err := streamingClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stream %s: %w", path, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, ErrUnauthorized
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("stream %s returned %d: %s", path, resp.StatusCode, body)
	}
	return resp.Body, nil
}

// newRequest builds an *http.Request with auth headers set.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("User-Agent", "astro-cli/dev")
	req.Header.Set("Accept", "application/json")
	return req, nil
}
