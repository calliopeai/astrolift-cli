package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const inboxReplyFields = `id messageId authorLabel body choices decision route deliveredAt wakeTaskId createdAt`
const inboxMessageFields = `id threadId kind source title body agentSlug agentName taskId taskStatus replyToId requestSequence expiresAt createdAt deliveredAt readAt answerable options { id label description } attachments { kind ref label } replies { ` + inboxReplyFields + ` }`
const inboxMessagesQuery = `query InboxMessages($threadId:GUID,$after:String,$first:Int!,$unreadOnly:Boolean!) { astroliftAgentMessages(threadId:$threadId,after:$after,first:$first,unreadOnly:$unreadOnly) { nextCursor totalCount unreadCount items { ` + inboxMessageFields + ` } } }`
const inboxMaxPages = 40
const inboxMaxBody = 32768

var inboxPollInterval = 5 * time.Second

type inboxReply struct {
	ID          string   `json:"id"`
	MessageID   string   `json:"messageId"`
	AuthorLabel string   `json:"authorLabel"`
	Body        string   `json:"body"`
	Choices     []string `json:"choices"`
	Decision    *string  `json:"decision"`
	Route       string   `json:"route"`
	DeliveredAt *string  `json:"deliveredAt"`
	WakeTaskID  *string  `json:"wakeTaskId"`
	CreatedAt   string   `json:"createdAt"`
}

type inboxMessage struct {
	ID              string  `json:"id"`
	ThreadID        string  `json:"threadId"`
	Kind            string  `json:"kind"`
	Source          string  `json:"source"`
	Title           string  `json:"title"`
	Body            string  `json:"body"`
	AgentSlug       *string `json:"agentSlug"`
	AgentName       *string `json:"agentName"`
	TaskID          *string `json:"taskId"`
	TaskStatus      *string `json:"taskStatus"`
	ReplyToID       *string `json:"replyToId"`
	RequestSequence *int    `json:"requestSequence"`
	ExpiresAt       *string `json:"expiresAt"`
	CreatedAt       string  `json:"createdAt"`
	DeliveredAt     *string `json:"deliveredAt"`
	ReadAt          *string `json:"readAt"`
	Answerable      bool    `json:"answerable"`
	Options         []struct {
		ID          string  `json:"id"`
		Label       string  `json:"label"`
		Description *string `json:"description"`
	} `json:"options"`
	Attachments []struct {
		Kind  string  `json:"kind"`
		Ref   string  `json:"ref"`
		Label *string `json:"label"`
	} `json:"attachments"`
	Replies []inboxReply `json:"replies"`
}

type inboxPage struct {
	Items       []inboxMessage `json:"items"`
	NextCursor  *string        `json:"nextCursor"`
	TotalCount  *int           `json:"totalCount"`
	UnreadCount int            `json:"unreadCount"`
}

func inboxPageRead(ctx context.Context, client *api.Client, thread, after string, limit int, unread bool) (*inboxPage, error) {
	vars := map[string]interface{}{"first": limit, "unreadOnly": unread}
	if thread != "" {
		vars["threadId"] = thread
	}
	if after != "" {
		vars["after"] = after
	}
	var response struct {
		Page *inboxPage `json:"astroliftAgentMessages"`
	}
	if err := client.GraphQL(ctx, inboxMessagesQuery, vars, &response); err != nil {
		return nil, err
	}
	if response.Page == nil {
		return nil, errors.New("server returned no inbox page")
	}
	return response.Page, nil
}

func inboxSelection(cmd *cobra.Command) (string, int, error) {
	thread := stringFlag(cmd, "thread")
	if thread != "" {
		if _, err := uuid.Parse(thread); err != nil {
			return "", 0, errors.New("--thread requires a thread GUID")
		}
	}
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 1 || limit > 100 {
		return "", 0, errors.New("--limit must be between 1 and 100")
	}
	return thread, limit, nil
}

func runInboxList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	thread, limit, err := inboxSelection(cmd)
	if err != nil {
		return err
	}
	page, err := inboxPageRead(ctx, client, thread, stringFlag(cmd, "after"), limit, boolFlag(cmd, "unread"))
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, page)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTHREAD\tKIND\tREAD\tTITLE")
	for _, m := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n", m.ID, m.ThreadID, m.Kind, m.ReadAt != nil, strings.Join(strings.Fields(m.Title), " "))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if page.NextCursor != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "More messages: pass --after %s\n", *page.NextCursor)
	}
	return nil
}

func findInboxMessage(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) (*inboxMessage, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, errors.New("message requires an exact GUID")
	}
	thread, limit, err := inboxSelection(cmd)
	if err != nil {
		return nil, err
	}
	after := ""
	cursors := map[string]bool{}
	for i := 0; i < inboxMaxPages; i++ {
		page, err := inboxPageRead(ctx, client, thread, after, limit, false)
		if err != nil {
			return nil, err
		}
		for _, m := range page.Items {
			if m.ID == id {
				return &m, nil
			}
		}
		if page.NextCursor == nil {
			return nil, errors.New("message not found in the authorized inbox")
		}
		after = *page.NextCursor
		if after == "" || cursors[after] {
			return nil, errors.New("server repeated an inbox cursor")
		}
		cursors[after] = true
	}
	return nil, errors.New("message lookup reached its page limit; narrow with --thread")
}

func runInboxShow(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
	message, err := findInboxMessage(cmd, ctx, client, id)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "mark-read") {
		var response struct {
			Result struct {
				OK     bool            `json:"ok"`
				Errors []mutationError `json:"errors"`
				Data   *struct {
					Marked int `json:"marked"`
				} `json:"data"`
			} `json:"markAgentMessagesRead"`
		}
		query := `mutation InboxMarkRead($ids:[GUID!]) { markAgentMessagesRead(ids:$ids) { ok errors { code message field } data { marked } } }`
		if err := client.GraphQL(ctx, query, map[string]interface{}{"ids": []string{id}}, &response); err != nil {
			return err
		}
		if !response.Result.OK || response.Result.Data == nil {
			return fmt.Errorf("marking message read: %s", firstDeployError(response.Result.Errors))
		}
		// Reread the original identity so readAt comes from the server's clock.
		message, err = findInboxMessage(cmd, ctx, client, id)
		if err != nil {
			return err
		}
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, message)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s [%s]\nMessage: %s\nThread: %s\nAnswerable: %t\n\n%s\n", message.Title, message.Kind, message.ID, message.ThreadID, message.Answerable, message.Body); err != nil {
		return err
	}
	for _, option := range message.Options {
		fmt.Fprintf(cmd.OutOrStdout(), "Choice %s: %s\n", option.ID, option.Label)
	}
	for _, reply := range message.Replies {
		fmt.Fprintf(cmd.OutOrStdout(), "\nReply %s [%s]: %s\n", reply.ID, reply.Route, reply.Body)
	}
	return nil
}

func inboxReplyInput(cmd *cobra.Command, id string) (map[string]interface{}, string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, "", errors.New("message requires an exact GUID")
	}
	body := stringFlag(cmd, "body")
	if boolFlag(cmd, "stdin") {
		if body != "" {
			return nil, "", errors.New("choose --body or --stdin")
		}
		raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), inboxMaxBody+1))
		if err != nil {
			return nil, "", err
		}
		body = string(raw)
	}
	if len(body) > inboxMaxBody {
		return nil, "", errors.New("reply exceeds 32768 bytes")
	}
	decision := stringFlag(cmd, "decision")
	if decision != "" && decision != "allow" && decision != "deny" {
		return nil, "", errors.New("--decision must be allow or deny")
	}
	choices, _ := cmd.Flags().GetStringSlice("choice")
	if strings.TrimSpace(body) == "" && len(choices) == 0 && decision == "" {
		return nil, "", errors.New("pass --body, --stdin, --choice or --decision")
	}
	requestID := stringFlag(cmd, "request-id")
	if requestID == "" {
		requestID = uuid.NewString()
	} else if _, err := uuid.Parse(requestID); err != nil {
		return nil, "", errors.New("--request-id requires a UUID")
	}
	input := map[string]interface{}{"messageId": id, "clientRequestId": requestID}
	if body != "" {
		input["body"] = body
	}
	if len(choices) > 0 {
		input["choices"] = choices
	}
	if decision != "" {
		input["decision"] = decision
	}
	return input, requestID, nil
}

func runInboxReply(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
	input, requestID, err := inboxReplyInput(cmd, id)
	if err != nil {
		return err
	}
	// Preserve this ID even if the connection drops after the server accepts it.
	fmt.Fprintf(cmd.ErrOrStderr(), "Reply request ID: %s; reuse --request-id with the same reply if the response is lost.\n", requestID)
	var response struct {
		Result struct {
			OK     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *inboxReply     `json:"data"`
		} `json:"replyToAgentMessage"`
	}
	query := `mutation InboxReply($input:AstroliftReplyToAgentMessageInput!) { replyToAgentMessage(input:$input) { ok errors { code message field } data { ` + inboxReplyFields + ` } } }`
	if err := client.GraphQL(ctx, query, map[string]interface{}{"input": input}, &response); err != nil {
		return err
	}
	if !response.Result.OK || response.Result.Data == nil {
		return fmt.Errorf("replying: %s", firstDeployError(response.Result.Errors))
	}
	reply := response.Result.Data
	if reply.MessageID != id || reply.ID == "" {
		return errors.New("server returned no receipt for the exact message; preserve the request ID")
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, struct {
			RequestID string      `json:"requestId"`
			Reply     *inboxReply `json:"reply"`
		}{requestID, reply})
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Reply accepted: %s (route: %s). Delivery and execution are separate; use inbox show %s --json to reread.\n", reply.ID, reply.Route, id)
	return err
}

func inboxRetryable(err error) bool {
	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == 429 || httpErr.Status >= 500
	}
	var graphErr *api.GraphQLResponseError
	if errors.As(err, &graphErr) {
		return graphErr.Status == 429 || graphErr.Status >= 500
	}
	var transport net.Error
	return errors.As(err, &transport)
}

func inboxWait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runInboxFollow(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	thread, limit, err := inboxSelection(cmd)
	if err != nil {
		return err
	}
	interval := inboxPollInterval
	if cmd.Flags().Changed("interval") {
		interval, _ = cmd.Flags().GetDuration("interval")
	}
	if interval < time.Second {
		return errors.New("--interval must be at least 1s")
	}
	seen := map[string]bool{}
	failures := 0
	for {
		// Every cycle starts at newest. Keyset cursors page older records, so they
		// cannot act as a live watermark; walk until a previously emitted ID.
		after := ""
		cursors := map[string]bool{}
		fresh := []inboxMessage{}
		complete := false
		for pageNum := 0; pageNum < inboxMaxPages; pageNum++ {
			page, readErr := inboxPageRead(ctx, client, thread, after, limit, false)
			if readErr != nil {
				err = readErr
				break
			}
			for _, message := range page.Items {
				if seen[message.ID] {
					complete = true
					break
				}
				fresh = append(fresh, message)
			}
			if complete || page.NextCursor == nil {
				complete = true
				break
			}
			after = *page.NextCursor
			if after == "" || cursors[after] {
				return errors.New("server repeated an inbox cursor")
			}
			cursors[after] = true
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !inboxRetryable(err) || failures >= 4 {
				return err
			}
			failures++
			fmt.Fprintf(cmd.ErrOrStderr(), "Inbox temporarily unavailable; retry %d/4.\n", failures)
			if inboxWait(ctx, time.Duration(1<<failures)*time.Second) != nil {
				return nil
			}
			err = nil
			continue
		}
		if !complete {
			return errors.New("follow reached its page limit; narrow with --thread to avoid missing messages")
		}
		failures = 0
		for i := len(fresh) - 1; i >= 0; i-- {
			message := fresh[i]
			if seen[message.ID] {
				continue
			}
			if len(seen) >= 10000 {
				return errors.New("follow reached 10000 messages; start a new follow session")
			}
			if boolFlag(cmd, "json") {
				err = json.NewEncoder(cmd.OutOrStdout()).Encode(message)
			} else {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s [%s] %s\n%s\n", message.ID, message.Kind, message.Title, message.Body)
			}
			if err != nil {
				return err
			}
			seen[message.ID] = true
		}
		if inboxWait(ctx, interval) != nil {
			return nil
		}
	}
}

var inboxCmd = &cobra.Command{Use: "inbox", Short: "Read and answer your agents' messages", Long: "Read the selected organization's messages addressed to your user identity. Uses your existing Astrolift credential and tenant scope. Server permissions govern every read and reply."}

func inboxCommand(use, short string, args cobra.PositionalArgs, run func(*cobra.Command, context.Context, *api.Client, []string) error) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: args, RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer cancel()
		client, _, _, err := loadActiveClient(ctx, boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		if err := requireServerFeature(ctx, client, "agents.messages", "agent inbox"); err != nil {
			return err
		}
		return run(cmd, ctx, client, args)
	}}
}

func init() {
	list := inboxCommand("list", "List messages addressed to you", cobra.NoArgs, func(c *cobra.Command, x context.Context, a *api.Client, _ []string) error {
		return runInboxList(c, x, a)
	})
	show := inboxCommand("show <message-guid>", "Read one exact message and its replies", cobra.ExactArgs(1), func(c *cobra.Command, x context.Context, a *api.Client, args []string) error {
		return runInboxShow(c, x, a, args[0])
	})
	reply := inboxCommand("reply <message-guid>", "Answer a question or approval with a retry-safe request", cobra.ExactArgs(1), func(c *cobra.Command, x context.Context, a *api.Client, args []string) error {
		return runInboxReply(c, x, a, args[0])
	})
	follow := inboxCommand("follow", "Follow new messages with bounded polling (JSON Lines with --json)", cobra.NoArgs, func(c *cobra.Command, x context.Context, a *api.Client, _ []string) error {
		return runInboxFollow(c, x, a)
	})
	for _, c := range []*cobra.Command{list, show, follow} {
		c.Flags().String("thread", "", "Exact thread GUID to read")
		c.Flags().Int("limit", 50, "Messages per page (1-100)")
	}
	list.Flags().Bool("unread", false, "Only unread messages")
	list.Flags().String("after", "", "Opaque pagination cursor from a prior page")
	show.Flags().Bool("mark-read", false, "Mark the exact message as read, then reread it")
	reply.Flags().String("body", "", "Reply text (use --stdin for private or multi-line text)")
	reply.Flags().Bool("stdin", false, "Read reply text from stdin (maximum 32768 bytes)")
	reply.Flags().StringSlice("choice", nil, "Question option ID; repeat or comma-separate for multiple choices")
	reply.Flags().String("decision", "", "Approval decision: allow or deny (server gates still apply)")
	reply.Flags().String("request-id", "", "UUID retained across retries; generated and printed before submission if omitted")
	follow.Flags().Duration("interval", 5*time.Second, "Polling interval (at least 1s)")
	inboxCmd.AddCommand(list, show, reply, follow)
	rootCmd.AddCommand(inboxCmd)
}
