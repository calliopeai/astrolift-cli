package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const inboxMessageID = "019a0000-0000-7000-8000-000000000001"
const inboxThreadID = "019a0000-0000-7000-8000-000000000002"
const inboxRequestID = "019a0000-0000-7000-8000-000000000003"

func inboxTestCmd() (*cobra.Command, *bytes.Buffer) {
	c, out := agentTestCmd()
	c.SetErr(out)
	for _, f := range []string{"thread", "after", "body", "decision", "request-id"} {
		c.Flags().String(f, "", "")
	}
	for _, f := range []string{"unread", "mark-read", "stdin"} {
		c.Flags().Bool(f, false, "")
	}
	c.Flags().Int("limit", 50, "")
	c.Flags().Duration("interval", 5*time.Second, "")
	c.Flags().StringSlice("choice", nil, "")
	return c, out
}

func inboxTestPage(items []map[string]interface{}, next interface{}) map[string]interface{} {
	return map[string]interface{}{"astroliftAgentMessages": map[string]interface{}{"items": items, "nextCursor": next, "totalCount": len(items), "unreadCount": len(items)}}
}

func inboxTestMessage(id string) map[string]interface{} {
	return map[string]interface{}{"id": id, "threadId": inboxThreadID, "kind": "question", "title": "Review needed", "body": "May I continue?", "answerable": true, "options": []map[string]string{{"id": "continue", "label": "Continue"}}, "attachments": []map[string]string{{"kind": "artifact", "ref": "artifact-id"}}, "replies": []interface{}{}}
}

func TestInboxListOwnCredentialsAndOpaqueCursor(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/app/gql/config/" || r.Header.Get("Authorization") != "Bearer person-token" || r.Header.Get("X-Astrolift-Organization") != "org-guid" {
			t.Errorf("credentials or organization scope wrong")
		}
		var req gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Variables["threadId"] != inboxThreadID || req.Variables["after"] != "opaque-page" || req.Variables["first"] != float64(12) || req.Variables["unreadOnly"] != true {
			t.Errorf("wrong variables: %#v", req.Variables)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": inboxTestPage([]map[string]interface{}{inboxTestMessage(inboxMessageID)}, "next-page")})
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "person-token", false)
	client.SetOrg("org-guid")
	c, out := inboxTestCmd()
	for key, value := range map[string]string{"thread": inboxThreadID, "after": "opaque-page", "limit": "12", "unread": "true", "json": "true"} {
		_ = c.Flags().Set(key, value)
	}
	if err := runInboxList(c, context.Background(), client); err != nil {
		t.Fatal(err)
	}
	var page inboxPage
	if err := json.Unmarshal(out.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != inboxMessageID || page.NextCursor == nil || *page.NextCursor != "next-page" {
		t.Fatalf("page metadata lost: %s", out)
	}
	if calls != 1 {
		t.Fatalf("unexpected read fanout: %d", calls)
	}
}

func TestInboxShowPagesExactGUIDAndMarkRead(t *testing.T) {
	reads, marks := 0, 0
	srv := featureServer(t, nil, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "markAgentMessagesRead") {
			marks++
			ids := req.Variables["ids"].([]interface{})
			if len(ids) != 1 || ids[0] != inboxMessageID || req.Variables["threadId"] != nil {
				t.Errorf("marked beyond exact selected ID: %#v", req.Variables)
			}
			return map[string]interface{}{"markAgentMessagesRead": map[string]interface{}{"ok": true, "data": map[string]interface{}{"marked": 1}}}
		}
		reads++
		if req.Variables["after"] == nil {
			return inboxTestPage([]map[string]interface{}{inboxTestMessage(uuid.NewString())}, "older")
		}
		if req.Variables["after"] != "older" {
			t.Errorf("cursor lost: %#v", req.Variables)
		}
		message := inboxTestMessage(inboxMessageID)
		if marks > 0 {
			message["readAt"] = "2026-10-17T12:00:00Z"
		}
		return inboxTestPage([]map[string]interface{}{message}, nil)
	})
	defer srv.Close()
	c, out := inboxTestCmd()
	_ = c.Flags().Set("mark-read", "true")
	_ = c.Flags().Set("json", "true")
	if err := runInboxShow(c, context.Background(), api.NewClient(srv.URL, "person", false), inboxMessageID); err != nil {
		t.Fatal(err)
	}
	if marks != 1 || reads != 4 || !strings.Contains(out.String(), "2026-10-17T12:00:00Z") {
		t.Fatalf("show did not reread marked exact identity: reads=%d marks=%d %s", reads, marks, out)
	}
}

func TestInboxShowBoundsAndNeverFallsBackToAnotherMessage(t *testing.T) {
	for _, repeated := range []bool{false, true} {
		t.Run(fmt.Sprint(repeated), func(t *testing.T) {
			calls := 0
			srv := featureServer(t, nil, func(req gqlRequest) map[string]interface{} {
				calls++
				cursor := fmt.Sprint(calls)
				if repeated {
					cursor = "same"
				}
				return inboxTestPage([]map[string]interface{}{inboxTestMessage(uuid.NewString())}, cursor)
			})
			defer srv.Close()
			c, _ := inboxTestCmd()
			err := runInboxShow(c, context.Background(), api.NewClient(srv.URL, "person", false), inboxMessageID)
			if err == nil {
				t.Fatal("unbounded or fallback lookup")
			}
			if repeated && calls != 2 {
				t.Fatalf("repeated cursor was not refused: %d", calls)
			}
			if !repeated && calls != inboxMaxPages {
				t.Fatalf("page limit not respected: %d", calls)
			}
		})
	}
}

func TestInboxReplyIdempotencyAndRouteReceipt(t *testing.T) {
	var inputs []map[string]interface{}
	srv := featureServer(t, nil, func(req gqlRequest) map[string]interface{} {
		input := req.Variables["input"].(map[string]interface{})
		inputs = append(inputs, input)
		return map[string]interface{}{"replyToAgentMessage": map[string]interface{}{"ok": true, "data": map[string]interface{}{"id": "reply-guid", "messageId": inboxMessageID, "route": "live_input", "deliveredAt": nil, "body": "Continue safely", "choices": []string{"continue"}}}}
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "person", false)
	c, out := inboxTestCmd()
	_ = c.Flags().Set("request-id", inboxRequestID)
	_ = c.Flags().Set("stdin", "true")
	_ = c.Flags().Set("choice", "continue")
	_ = c.Flags().Set("json", "true")
	c.SetIn(strings.NewReader("Continue safely"))
	for i := 0; i < 2; i++ {
		c.SetIn(strings.NewReader("Continue safely"))
		if err := runInboxReply(c, context.Background(), client, inboxMessageID); err != nil {
			t.Fatal(err)
		}
	}
	if len(inputs) != 2 {
		t.Fatal("missing reply attempts")
	}
	for _, input := range inputs {
		if input["messageId"] != inboxMessageID || input["clientRequestId"] != inboxRequestID || input["body"] != "Continue safely" {
			t.Fatalf("retry identity changed: %#v", input)
		}
	}
	if strings.Contains(out.String(), "delivered successfully") || !strings.Contains(out.String(), `"route": "live_input"`) || !strings.Contains(out.String(), `"deliveredAt": null`) {
		t.Fatalf("route receipt misrepresented: %s", out)
	}
}

func TestInboxReplyPreservesRequestIDAfterLostResponse(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()
	c, out := inboxTestCmd()
	_ = c.Flags().Set("body", "test")
	if err := runInboxReply(c, context.Background(), api.NewClient(srv.URL, "person", false), inboxMessageID); err == nil {
		t.Fatal("lost response claimed success")
	}
	if calls != 1 {
		t.Fatalf("write automatically retried: %d", calls)
	}
	fields := strings.Fields(out.String())
	if len(fields) < 4 {
		t.Fatalf("request ID unavailable: %s", out)
	}
	if _, err := uuid.Parse(strings.TrimSuffix(fields[3], ";")); err != nil {
		t.Fatalf("recovery UUID missing before mutation: %s", out)
	}
}

func TestInboxReplyValidationAndRefusals(t *testing.T) {
	for _, tc := range []struct{ flag, value string }{{"body", strings.Repeat("a", inboxMaxBody+1)}, {"decision", "approve"}, {"request-id", "invalid"}} {
		c, _ := inboxTestCmd()
		_ = c.Flags().Set("body", "reply")
		_ = c.Flags().Set(tc.flag, tc.value)
		if _, _, err := inboxReplyInput(c, inboxMessageID); err == nil {
			t.Errorf("accepted invalid %s", tc.flag)
		}
	}
	c, _ := inboxTestCmd()
	_ = c.Flags().Set("stdin", "true")
	c.SetIn(strings.NewReader(strings.Repeat("x", inboxMaxBody+1)))
	if _, _, err := inboxReplyInput(c, inboxMessageID); err == nil {
		t.Fatal("oversized stdin accepted")
	}
	for _, code := range []string{"NOT_FOUND", "PERMISSION_DENIED", "EXPIRED", "VALIDATION", "PRECONDITION"} {
		t.Run(code, func(t *testing.T) {
			srv := featureServer(t, nil, func(gqlRequest) map[string]interface{} {
				return map[string]interface{}{"replyToAgentMessage": map[string]interface{}{"ok": false, "errors": []map[string]string{{"code": code, "message": "reply refused"}}}}
			})
			defer srv.Close()
			c, out := inboxTestCmd()
			_ = c.Flags().Set("body", "reply")
			if err := runInboxReply(c, context.Background(), api.NewClient(srv.URL, "person", false), inboxMessageID); err == nil {
				t.Fatal("refusal claimed success")
			}
			if strings.Contains(out.String(), "accepted") {
				t.Fatalf("false acceptance: %s", out)
			}
		})
	}
}

func TestInboxFollowReconcilesPagesWithoutDuplicatesAndCancels(t *testing.T) {
	saved := inboxPollInterval
	inboxPollInterval = time.Second
	t.Cleanup(func() { inboxPollInterval = saved })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	heads := 0
	oldID, newID := uuid.NewString(), uuid.NewString()
	srv := featureServer(t, nil, func(req gqlRequest) map[string]interface{} {
		if req.Variables["after"] == nil {
			heads++
			if heads == 1 {
				return inboxTestPage([]map[string]interface{}{inboxTestMessage(inboxMessageID)}, "older")
			}
			if heads == 2 {
				return inboxTestPage([]map[string]interface{}{inboxTestMessage(newID), inboxTestMessage(inboxMessageID)}, nil)
			}
			cancel()
			return inboxTestPage([]map[string]interface{}{inboxTestMessage(newID)}, nil)
		}
		return inboxTestPage([]map[string]interface{}{inboxTestMessage(oldID)}, nil)
	})
	defer srv.Close()
	c, out := inboxTestCmd()
	_ = c.Flags().Set("json", "true")
	if err := runInboxFollow(c, ctx, api.NewClient(srv.URL, "person", false)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{oldID, inboxMessageID, newID} {
		if strings.Count(out.String(), `"id":"`+id+`"`) != 1 {
			t.Fatalf("duplicate/missing event %s: %s", id, out)
		}
	}
	if strings.Index(out.String(), oldID) > strings.Index(out.String(), inboxMessageID) {
		t.Fatalf("initial backfill not oldest first: %s", out)
	}
}

func TestInboxFollowRefusesUnboundedWindowAndPermissionRetry(t *testing.T) {
	for _, permission := range []bool{false, true} {
		t.Run(fmt.Sprint(permission), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if permission {
					w.WriteHeader(403)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": inboxTestPage([]map[string]interface{}{inboxTestMessage(uuid.NewString())}, fmt.Sprint(calls))})
			}))
			defer srv.Close()
			c, out := inboxTestCmd()
			err := runInboxFollow(c, context.Background(), api.NewClient(srv.URL, "person", false))
			if err == nil {
				t.Fatal("follow succeeded despite refusal/gap")
			}
			if permission && calls != 1 {
				t.Fatalf("permission denied retried %d times", calls)
			}
			if !permission && (calls != inboxMaxPages || out.Len() != 0) {
				t.Fatalf("truncated window emitted: calls=%d output=%s", calls, out)
			}
		})
	}
	if inboxRetryable(&api.HTTPError{Status: 403}) || inboxRetryable(&api.HTTPError{Status: 401}) || inboxRetryable(api.ErrSchemaMismatch) {
		t.Fatal("permanent error classified retryable")
	}
	if !inboxRetryable(&api.HTTPError{Status: 429}) || !inboxRetryable(&api.HTTPError{Status: 503}) {
		t.Fatal("transient refusal not retryable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(inboxWait(ctx, time.Hour), context.Canceled) {
		t.Fatal("backoff ignores cancellation")
	}
}

func TestInboxCapabilityMissingAndCommandsRegistered(t *testing.T) {
	writes := 0
	srv := featureServer(t, nil, func(gqlRequest) map[string]interface{} { writes++; return nil })
	defer srv.Close()
	if err := requireServerFeature(context.Background(), api.NewClient(srv.URL, "person", false), "agents.messages", "agent inbox"); err == nil || !strings.Contains(err.Error(), "does not support agent inbox") {
		t.Fatalf("old server guidance: %v", err)
	}
	if writes != 0 {
		t.Fatalf("unsupported server touched inbox: %d", writes)
	}
	for _, name := range []string{"list", "show", "reply", "follow"} {
		found := false
		for _, c := range inboxCmd.Commands() {
			if c.Name() == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing inbox %s", name)
		}
	}
}
