// Package cmd — `astro exec ...` : run a command (or interactive shell)
// inside a running container, over the control-plane exec WebSocket relay.
//
// The relay (`/app/exec/<app>/<pod>`, backend core.schema.exec_ws) is the
// same surface the web console uses. It authenticates the handshake with
// the active `alft_` API token (bearer), enforces the `app.exec_pod`
// permission server-side, and audits every opened session. The frame
// protocol is JSON control frames both ways:
//
//	→ {"type":"open","command":[...],"container":"..."}
//	→ {"type":"stdin","data":"..."}  {"type":"resize","rows":R,"cols":C}  {"type":"close"}
//	← {"type":"ready"} {"type":"stdout","data":...} {"type":"stderr",...} {"type":"exit","code":N}
//
// Issue: calliopeai/astrolift#1040
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	execApp       string
	execWorkload  string
	execPod       string
	execContainer string
	execNoTTY     bool
)

// astroliftAppPodsQuery lists the app's pods so exec can resolve a target
// pod (and its primary container) when --pod isn't given.
const astroliftAppPodsQuery = `query($appSlug: String!) {
  astroliftAppPods(appSlug: $appSlug) {
    name
    workload
    phase
    ready
    containerStatuses { name }
  }
}`

type execPodInfo struct {
	Name              string `json:"name"`
	Workload          string `json:"workload"`
	Phase             string `json:"phase"`
	Ready             bool   `json:"ready"`
	ContainerStatuses []struct {
		Name string `json:"name"`
	} `json:"containerStatuses"`
}

var execCmd = &cobra.Command{
	Use:   "exec --app <slug> [--workload <w>] [--pod <p>] [-c <container>] -- <command...>",
	Short: "Run a command or interactive shell in a running container",
	Long: `Opens an exec session into a running pod for the app, streaming over
the control-plane WebSocket relay (the same one the web console uses).
Requires the app.exec_pod permission; every session is audited.

With no trailing command, runs an interactive shell ('sh'). With a
'-- <command...>' suffix, runs that command. A TTY is allocated when
stdin is a terminal; pass --no-tty to force a non-interactive pipe.

The target pod is auto-resolved (first ready/Running pod for the app);
narrow it with --workload, or pin an exact pod with --pod. Use
--container for multi-container pods.

Examples:
  astro exec --app web -- bash
  astro exec --app web -- python manage.py migrate
  astro exec --app web --pod web-7c9f-abc -c sidecar -- sh
  echo "select 1" | astro exec --app web --no-tty -- psql "$DATABASE_URL"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runExec(cmd, cmd.Context(), client, args)
	},
}

func runExec(cmd *cobra.Command, ctx context.Context, client *api.Client, command []string) error {
	if execApp == "" {
		return fmt.Errorf("--app is required")
	}

	// Resolve the target pod. --pod wins; otherwise pick a ready/Running
	// pod for the app (optionally filtered to --workload) so the common
	// `astro exec --app X -- sh` just works.
	pod := execPod
	container := execContainer
	if pod == "" {
		rp, rc, err := resolveExecPod(ctx, client)
		if err != nil {
			return err
		}
		pod = rp
		if container == "" {
			container = rc
		}
	}
	if pod == "" {
		return fmt.Errorf("no running pod found for app %q (try --pod)", execApp)
	}

	wsURL, err := execWSURL(client.BaseURL(), execApp, pod)
	if err != nil {
		return err
	}

	header := http.Header{}
	if tok := client.Token(); tok != "" {
		header.Set("Authorization", "Bearer "+tok)
	}
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return fmt.Errorf("exec unauthorized — run `astro auth login`")
			case http.StatusForbidden:
				return fmt.Errorf("exec denied — you need the app.exec_pod permission")
			}
		}
		return fmt.Errorf("connecting exec socket (%s): %w", wsURL, err)
	}
	defer conn.Close()

	if len(command) == 0 {
		command = []string{"sh"}
	}
	stdinFd := int(os.Stdin.Fd())
	wantTTY := !execNoTTY && term.IsTerminal(stdinFd)

	var restore func()
	if wantTTY {
		if oldState, merr := term.MakeRaw(stdinFd); merr == nil {
			restore = func() { _ = term.Restore(stdinFd, oldState) }
			defer restore()
		}
	}

	var writeMu sync.Mutex
	writeJSON := func(v interface{}) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(v)
	}

	if err := writeJSON(map[string]interface{}{
		"type":      "open",
		"command":   command,
		"container": container,
		// Ask for a PTY only when we're interactive; piped/non-interactive
		// runs get a plain pipe so output isn't echo-doubled or CRLF-mangled.
		"tty": wantTTY,
	}); err != nil {
		return fmt.Errorf("sending open: %w", err)
	}

	// Window size: send once + on every SIGWINCH.
	sendResize := func() {
		if !wantTTY {
			return
		}
		cols, rows, gerr := term.GetSize(stdinFd)
		if gerr != nil {
			return
		}
		_ = writeJSON(map[string]interface{}{"type": "resize", "rows": rows, "cols": cols})
	}
	sendResize()
	if wantTTY {
		// SIGWINCH is Unix-only, so the resize watcher lives in
		// platform-tagged files (no-op on Windows). See exec_resize_*.go.
		defer watchResize(sendResize)()
	}

	// stdin → stdin frames (best-effort; ends on EOF/error with a close).
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := os.Stdin.Read(buf)
			if n > 0 {
				if werr := writeJSON(map[string]interface{}{"type": "stdin", "data": string(buf[:n])}); werr != nil {
					return
				}
			}
			if rerr != nil {
				// stdin EOF: signal the remote to half-close its stdin so a
				// piped read-to-EOF command (cat, psql < script) sees EOF and
				// finishes — but keep the session open for its output + exit
				// frame (don't send a full close, which would cut output on
				// the common non-interactive `astro exec -- cmd`).
				_ = writeJSON(map[string]interface{}{"type": "stdin_eof"})
				return
			}
		}
	}()

	// Read loop → terminal. Returns the container's exit code as the
	// process exit status so scripts can branch on it.
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	for {
		_, data, rerr := conn.ReadMessage()
		if rerr != nil {
			return nil
		}
		var frame struct {
			Type    string `json:"type"`
			Data    string `json:"data"`
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		switch frame.Type {
		case "stdout":
			fmt.Fprint(out, frame.Data)
		case "stderr":
			fmt.Fprint(errOut, frame.Data)
		case "error":
			fmt.Fprintln(errOut, "exec error: "+frame.Message)
		case "exit":
			if restore != nil {
				restore()
			}
			// Propagate the remote command's exit code as our own (like
			// ssh / kubectl exec) instead of collapsing to 1. os.Exit is
			// the sanctioned exception here (cf. cmd/ci.go configErr) — the
			// command ran and its output already streamed; only the status
			// remains to forward. Deferred conn.Close won't run under
			// os.Exit, so close explicitly first.
			if frame.Code != 0 {
				_ = conn.Close()
				os.Exit(frame.Code)
			}
			return nil
		}
	}
}

// resolveExecPod lists the app's pods and returns the best exec target
// (name + primary container): a ready/Running pod wins; otherwise the
// first pod. --workload narrows the set.
func resolveExecPod(ctx context.Context, client *api.Client) (string, string, error) {
	var resp struct {
		Pods []execPodInfo `json:"astroliftAppPods"`
	}
	if err := client.GraphQL(ctx, astroliftAppPodsQuery,
		map[string]interface{}{"appSlug": execApp}, &resp); err != nil {
		return "", "", fmt.Errorf("listing pods: %w", err)
	}

	var candidates []execPodInfo
	for _, p := range resp.Pods {
		if execWorkload != "" && p.Workload != execWorkload {
			continue
		}
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		suffix := ""
		if execWorkload != "" {
			suffix = fmt.Sprintf(" (workload %q)", execWorkload)
		}
		return "", "", fmt.Errorf("no running pods for app %q%s", execApp, suffix)
	}

	pick := candidates[0]
	for _, p := range candidates {
		if p.Ready && strings.EqualFold(p.Phase, "Running") {
			pick = p
			break
		}
	}
	container := ""
	if len(pick.ContainerStatuses) > 0 {
		container = pick.ContainerStatuses[0].Name
	}
	return pick.Name, container, nil
}

// execWSURL turns the HTTP API base into the ws(s):// exec endpoint for
// an app/pod pair, taking only scheme+host from the base so it works
// whether the configured api_url includes a path or not.
func execWSURL(base, app, pod string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid server URL %q: %w", base, err)
	}
	scheme := "wss"
	if u.Scheme == "http" {
		scheme = "ws"
	}
	return fmt.Sprintf("%s://%s/app/exec/%s/%s",
		scheme, u.Host, url.PathEscape(app), url.PathEscape(pod)), nil
}

func init() {
	execCmd.Flags().StringVar(&execApp, "app", "", "App slug to exec into (required)")
	execCmd.Flags().StringVar(&execWorkload, "workload", "", "Narrow pod selection to a workload")
	execCmd.Flags().StringVar(&execPod, "pod", "", "Exact pod name (skips auto-resolution)")
	execCmd.Flags().StringVarP(&execContainer, "container", "c", "", "Container name (multi-container pods)")
	execCmd.Flags().BoolVar(&execNoTTY, "no-tty", false, "Force non-interactive (no TTY) even on a terminal")
	rootCmd.AddCommand(execCmd)
}
