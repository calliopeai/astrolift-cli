package api

import (
	"fmt"
	"net/http"
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
		baseURL: baseURL,
		token:   token,
		debug:   debug,
	}
}

// GraphQL sends a GraphQL query to the platform API and decodes the response
// into the provided target. Variables are passed as a map.
func (c *Client) GraphQL(query string, variables map[string]interface{}, target interface{}) error {
	_ = query
	_ = variables
	_ = target
	return fmt.Errorf("not implemented")
}

// Get performs an authenticated GET request against the REST API.
func (c *Client) Get(path string, target interface{}) error {
	_ = path
	_ = target
	return fmt.Errorf("not implemented")
}

// Post performs an authenticated POST request against the REST API.
func (c *Client) Post(path string, body interface{}, target interface{}) error {
	_ = path
	_ = body
	_ = target
	return fmt.Errorf("not implemented")
}

// newRequest builds an *http.Request with auth headers set.
func (c *Client) newRequest(method, path string, body interface{}) (*http.Request, error) {
	_ = method
	_ = path
	_ = body
	return nil, fmt.Errorf("not implemented")
}
