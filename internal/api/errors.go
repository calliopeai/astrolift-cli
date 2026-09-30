package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// GraphQLError preserves only the public diagnostic fields. Arbitrary server
// extensions and response bodies may contain credentials and are never echoed.
type GraphQLError struct {
	Message          string `json:"message"`
	Code             string `json:"code,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Path             []any  `json:"path,omitempty"`
	CurrentVersion   *int   `json:"currentVersion,omitempty"`
	RequestedVersion *int   `json:"requestedVersion,omitempty"`
}

type GraphQLResponseError struct {
	Status int            `json:"status"`
	Errors []GraphQLError `json:"errors"`
}

func (e *GraphQLResponseError) Error() string {
	messages := make([]string, len(e.Errors))
	for i, item := range e.Errors {
		messages[i] = item.Message
		if item.Code != "" {
			messages[i] = item.Code + ": " + messages[i]
		}
		if item.Reason != "" && item.Reason != item.Message {
			messages[i] += " (" + item.Reason + ")"
		}
	}
	return "graphql errors: " + strings.Join(messages, "; ")
}

func (e *GraphQLResponseError) Unwrap() error {
	if e.Status == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if e.Status >= 400 && e.Status != http.StatusBadRequest {
		return nil
	}
	var messages []string
	for _, item := range e.Errors {
		// A declared operational code must not trigger a compatibility fallback,
		// even if its message happens to mention an unknown field.
		if item.Code != "" && item.Code != "GRAPHQL_VALIDATION_FAILED" {
			return nil
		}
		messages = append(messages, item.Message)
	}
	if isSchemaMismatch(messages) {
		return ErrSchemaMismatch
	}
	return nil
}

// HTTPError reports transport refusal without including an untrusted body.
type HTTPError struct {
	Status int `json:"status"`
}

func (e *HTTPError) Error() string {
	if e.Status == http.StatusUnauthorized {
		return ErrUnauthorized.Error()
	}
	return fmt.Sprintf("graphql HTTP %d: %s", e.Status, http.StatusText(e.Status))
}

func (e *HTTPError) Unwrap() error {
	if e.Status == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	return nil
}

func (c *Client) graphQLError(raw json.RawMessage) GraphQLError {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return GraphQLError{Message: "invalid GraphQL error"}
	}
	var item GraphQLError
	_ = json.Unmarshal(fields["message"], &item.Message)
	item.CurrentVersion = optionalErrorVersion(fields["currentVersion"])
	item.RequestedVersion = optionalErrorVersion(fields["requestedVersion"])
	var extensions map[string]json.RawMessage
	_ = json.Unmarshal(fields["extensions"], &extensions)
	_ = json.Unmarshal(extensions["code"], &item.Code)
	_ = json.Unmarshal(extensions["reason"], &item.Reason)
	if item.CurrentVersion == nil {
		item.CurrentVersion = optionalErrorVersion(extensions["currentVersion"])
	}
	if item.RequestedVersion == nil {
		item.RequestedVersion = optionalErrorVersion(extensions["requestedVersion"])
	}
	var path []any
	_ = json.Unmarshal(fields["path"], &path)
	for _, part := range path {
		switch value := part.(type) {
		case string:
			item.Path = append(item.Path, c.redactCredential(value))
		case float64:
			if value >= 0 && value == float64(int(value)) {
				item.Path = append(item.Path, int(value))
			}
		}
	}
	item.Message = c.redactCredential(item.Message)
	item.Code = c.redactCredential(item.Code)
	item.Reason = c.redactCredential(item.Reason)
	return item
}

func optionalErrorVersion(raw json.RawMessage) *int {
	var version *int
	if json.Unmarshal(raw, &version) != nil {
		return nil
	}
	return version
}

// RedactDiagnostic removes the current credential if the server reflects it in
// a permitted text field. Commands also apply it to informational trace text.
func (c *Client) RedactDiagnostic(text string) string { return c.redactCredential(text) }

func (c *Client) redactCredential(text string) string {
	if c.token != "" {
		text = strings.ReplaceAll(text, c.token, "[redacted]")
	}
	return text
}
