package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahp"
	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
	"github.com/microsoft/agent-host-protocol/clients/go/ahpws"
)

// AgentHostProtocolVersion is pinned to the control plane's projection.
// The SDK supplies transport and typed messages; the attach subset is tested
// against the pinned 1.0.0 wire rather than its default version advertisement.
const AgentHostProtocolVersion = "1.0.0"

type AgentHostUnavailable struct {
	Reason string
}

func (e *AgentHostUnavailable) Error() string { return "agent host unavailable: " + e.Reason }

type AgentHostSnapshot struct {
	Resource string          `json:"resource"`
	State    json.RawMessage `json:"state"`
	FromSeq  int64           `json:"fromSeq"`
}

// AgentHostAction preserves the negotiated wire's complete action body.
// The released SDK's 0.9 chat/error shape differs from protocol 1.0.
type AgentHostAction struct {
	Channel         string                 `json:"channel"`
	Action          json.RawMessage        `json:"action"`
	ServerSeq       int64                  `json:"serverSeq"`
	Origin          *ahptypes.ActionOrigin `json:"origin,omitempty"`
	RejectionReason *string                `json:"rejectionReason,omitempty"`
}

// Use the official SDK for transport and requests while retaining notification
// payloads before its version-specific decoder. Blocking delivery provides
// backpressure; the SDK's own subscription fan-out silently drops overflow.
type agentHostTransport struct {
	ahp.Transport
	channel   string
	channelMu sync.RWMutex
	actions   chan AgentHostAction
	closeOnce sync.Once
}

func (t *agentHostTransport) Recv(ctx context.Context) (ahp.TransportMessage, error) {
	message, err := t.Transport.Recv(ctx)
	if err != nil {
		t.closeOnce.Do(func() { close(t.actions) })
		return message, err
	}
	parsed, err := message.IntoParsed()
	if err != nil {
		return message, err
	}
	if parsed.Notification != nil && parsed.Notification.Method == "action" {
		var action AgentHostAction
		if err := json.Unmarshal(parsed.Notification.Params, &action); err != nil {
			return message, err
		}
		t.channelMu.RLock()
		selected := action.Channel == t.channel
		t.channelMu.RUnlock()
		if selected {
			select {
			case t.actions <- action:
			case <-ctx.Done():
				return message, ctx.Err()
			}
		}
	}
	return message, nil
}

func (t *agentHostTransport) selectChannel(channel string) {
	t.channelMu.Lock()
	defer t.channelMu.Unlock()
	t.channel = channel
}

type AgentSession struct {
	client        *ahp.Client
	transport     *agentHostTransport
	channel       string
	clientID      string
	nextClientSeq int64
	Snapshot      AgentHostSnapshot
}

func (c *Client) AttachAgentSession(ctx context.Context, taskID, boxID, clientID string) (*AgentSession, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return nil, fmt.Errorf("agent host requires an HTTP or HTTPS server")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/app/ahp"
	u.RawQuery, u.Fragment = "", ""
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.token)
	headers.Set("X-Astrolift-Organization", c.orgID)
	transport, err := ahpws.Connect(ctx, u.String(), ahpws.DialOptions{HTTPHeader: headers})
	if err != nil {
		return nil, fmt.Errorf("connect agent host: %w", err)
	}
	wire := &agentHostTransport{Transport: transport, actions: make(chan AgentHostAction, 256)}
	client, err := ahp.Connect(ctx, wire, ahp.DefaultConfig())
	if err != nil {
		_ = transport.Close(ctx)
		return nil, err
	}
	closeOnError := func(err error) (*AgentSession, error) {
		_ = client.Shutdown(context.Background())
		return nil, agentHostError(err)
	}
	init, err := client.Initialize(ctx, clientID, []string{AgentHostProtocolVersion}, []string{ahptypes.RootResourceURI})
	if err != nil {
		return closeOnError(err)
	}
	if init.ProtocolVersion != AgentHostProtocolVersion {
		return closeOnError(fmt.Errorf("agent host selected unsupported protocol %q", init.ProtocolVersion))
	}
	nextSeq := int64(1)
	for _, snapshot := range init.Snapshots {
		if root := snapshot.State.Root; root != nil {
			if raw, ok := root.Meta["astrolift.nextClientSeq"]; ok {
				_ = json.Unmarshal(raw, &nextSeq)
			}
		}
	}
	if nextSeq < 1 {
		return closeOnError(fmt.Errorf("agent host returned an invalid client sequence"))
	}
	channel := "ahp-session:/" + taskID + "/chat"
	if boxID != "" {
		channel = ""
		for _, snapshot := range init.Snapshots {
			if root := snapshot.State.Root; root != nil {
				for _, terminal := range root.Terminals {
					if strings.HasPrefix(terminal.Resource, "ahp-terminal:/"+boxID+"/") {
						channel = terminal.Resource
						break
					}
				}
			}
		}
		if channel == "" {
			return closeOnError(fmt.Errorf("box terminal unavailable or permission denied"))
		}
	}
	wire.selectChannel(channel)
	var result struct {
		Snapshot AgentHostSnapshot `json:"snapshot"`
	}
	if err := client.Request(ctx, "subscribe", ahptypes.SubscribeParams{Channel: channel}, &result); err != nil {
		return closeOnError(err)
	}
	return &AgentSession{client: client, transport: wire, channel: channel, clientID: clientID, Snapshot: result.Snapshot, nextClientSeq: nextSeq}, nil
}

func agentHostError(err error) error {
	var rpc *ahp.RPCError
	if errors.As(err, &rpc) {
		var metadata struct {
			Available *bool  `json:"ahp_available"`
			Reason    string `json:"reason"`
		}
		if json.Unmarshal(rpc.Data, &metadata) == nil && metadata.Available != nil && !*metadata.Available {
			return &AgentHostUnavailable{Reason: metadata.Reason}
		}
	}
	return err
}

func (s *AgentSession) Events() <-chan AgentHostAction { return s.transport.actions }

// Dispatch awaits the authoritative echo, including a rejection reason. It
// never treats writing a notification as proof that steering was accepted.
func (s *AgentSession) Dispatch(ctx context.Context, raw json.RawMessage, other func(AgentHostAction)) (*AgentHostAction, error) {
	var action ahptypes.StateAction
	if err := json.Unmarshal(raw, &action); err != nil {
		return nil, fmt.Errorf("invalid AHP action: %w", err)
	}
	seq := s.nextClientSeq
	if err := s.client.Notify(ctx, "dispatchAction", map[string]interface{}{"channel": s.channel, "clientSeq": seq, "action": raw}); err != nil {
		return nil, err
	}
	s.nextClientSeq++
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		select {
		case <-wait.Done():
			return nil, fmt.Errorf("action acknowledgment was not received; the action may have been accepted; inspect the session before resending: %w", wait.Err())
		case event, ok := <-s.Events():
			if !ok {
				return nil, errors.New("agent host disconnected before acknowledging the action")
			}
			if event.Origin != nil && event.Origin.ClientId == s.clientID && event.Origin.ClientSeq == seq {
				if event.RejectionReason != nil {
					return &event, fmt.Errorf("agent host rejected action: %s", *event.RejectionReason)
				}
				return &event, nil
			}
			if other != nil {
				other(event)
			}
		}
	}
}

// Close detaches the transport. It sends no stop or process-disposal action.
func (s *AgentSession) Close(ctx context.Context) error { return s.client.Shutdown(ctx) }
