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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	execApp         string
	execWorkload    string
	execPod         string
	execContainer   string
	execNoTTY       bool
	execEnvironment string
)

// execTerminal is exactly the slice of golang.org/x/term that a session
// depends on. It exists as an interface so a test can drive a session that
// believes it holds a TTY without allocating a pty, which is what kept
// raw-mode handling, resize frames and interrupt forwarding untestable
// (#78). Production wiring is the real package; nothing else implements it.
type execTerminal interface {
	IsTerminal(fd int) bool
	GetSize(fd int) (width, height int, err error)
	MakeRaw(fd int) (*term.State, error)
	Restore(fd int, state *term.State) error
}

type xTerm struct{}

func (xTerm) IsTerminal(fd int) bool              { return term.IsTerminal(fd) }
func (xTerm) GetSize(fd int) (int, int, error)    { return term.GetSize(fd) }
func (xTerm) MakeRaw(fd int) (*term.State, error) { return term.MakeRaw(fd) }
func (xTerm) Restore(fd int, s *term.State) error { return term.Restore(fd, s) }

// Process handles a session reaches for, named so tests can substitute
// them. execExit stays os.Exit in production: forwarding the remote's
// status is the documented behaviour of the exit frame below, and a test
// that let it run would take the test binary down with it.
var (
	execStdin io.Reader    = os.Stdin
	execExit  func(int)    = os.Exit
	execTerm  execTerminal = xTerm{}
)

// astroliftAppPodsQuery lists the app's pods so exec can resolve a target
// pod (and its primary container) when --pod isn't given.
const astroliftAppPodsQuery = `query($appSlug: String!) {
  astroliftAppPods(appSlug: $appSlug) {
    name
    workload
    phase
    ready
    containerStatuses { name kind }
  }
}`

type execPodInfo struct {
	Name              string `json:"name"`
	Workload          string `json:"workload"`
	Phase             string `json:"phase"`
	Ready             bool   `json:"ready"`
	ContainerStatuses []struct {
		Name string `json:"name"`
		// Kind is init, primary or sidecar, as the control plane classifies
		// the container against the pod spec.
		Kind string `json:"kind"`
	} `json:"containerStatuses"`
}

// primaryContainer names the container a session opens when --container is
// not given: the one the control plane classifies as the pod's primary.
//
// Not the first entry. The control plane lists init containers first and the
// kubelet orders the rest by name, so the first entry is an init container
// or a sidecar whenever the pod has one. Opening a session in a finished init
// container fails inside the cluster after the WebSocket upgrade, and the
// relay reports that as a bare exit code 1 (DV-9250).
//
// Empty when no container is marked primary, which leaves the choice to
// Kubernetes: the only container of a single-container pod, or a refusal
// that names the containers.
func (p execPodInfo) primaryContainer() string {
	for _, c := range p.ContainerStatuses {
		if c.Kind == "primary" {
			return c.Name
		}
	}
	return ""
}

// readyAndRunning is the bar auto-resolution holds a pod to. Anything less
// has no running container to open a session in.
func (p execPodInfo) readyAndRunning() bool {
	return p.Ready && strings.EqualFold(p.Phase, "Running")
}

// describeExecPods lists pods for an error message: name, phase and
// readiness, so the reader sees why none qualified and has a name to pass
// to --pod.
func describeExecPods(pods []execPodInfo) string {
	const shown = 10
	parts := make([]string, 0, shown+1)
	for i, p := range pods {
		if i == shown {
			parts = append(parts, fmt.Sprintf("and %d more", len(pods)-shown))
			break
		}
		phase := p.Phase
		if phase == "" {
			phase = "phase unknown"
		}
		ready := "not ready"
		if p.Ready {
			ready = "ready"
		}
		parts = append(parts, fmt.Sprintf("%s (%s, %s)", p.Name, phase, ready))
	}
	return strings.Join(parts, ", ")
}

// podCount is "1 pod" or "N pods".
func podCount(n int) string {
	if n == 1 {
		return "1 pod"
	}
	return fmt.Sprintf("%d pods", n)
}

// execPodWorkloads names the workloads that do have pods, for the error that
// says --workload matched none of them: "workload web" or "workloads web,
// worker".
func execPodWorkloads(pods []execPodInfo) string {
	var names []string
	seen := map[string]bool{}
	for _, p := range pods {
		name := p.Workload
		if name == "" {
			name = "(none)"
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) == 1 {
		return "workload " + names[0]
	}
	return "workloads " + strings.Join(names, ", ")
}

var execCmd = &cobra.Command{
	Use:   "exec --app <slug> [--workload <w>] [--pod <p>] [-c <container>] -- <command...>",
	Short: "Run a command or interactive shell in a running container",
	Long: `Opens an exec session into a running pod for the app, streaming over
the control-plane WebSocket relay (the same one the web console uses).
Requires the app.exec_pod permission. The current server records best-effort
audit metadata after opening; this is not durable admission before execution.

With no trailing command, runs an interactive shell ('sh'). With a
'-- <command...>' suffix, runs that command. A TTY is allocated when
stdin is a terminal; pass --no-tty to force a non-interactive pipe.

Without --pod the target is auto-resolved: the first ready, Running pod
for the app in its default environment (narrow it with --workload), and
that pod's primary container (choose another with --container). When
nothing qualifies, exec says where it looked and lists the pods it found;
--pod pins an exact pod, ready or not. A non-zero exit is reported on
stderr with its pod and container. With --environment <GUID>, all selections
belong to that exact environment and the API must provide a complete reviewed
target. The client waits for the server to admit that target before reading stdin.
Failures never fall back to the primary environment. A lost exit receipt returns
an error with the last input outcome unknown; reconnect and input replay are not
supported. Pod UID checking is preflight only, not atomic pod binding.

Interrupts and detaching
  In an interactive session Ctrl-C interrupts the REMOTE process, the
  same as ssh or kubectl exec. It does not end the session and it does
  not kill this client. That holds whether or not the terminal could be
  put in raw mode.

  To leave, end the session: exit the remote shell, or close the
  connection (Ctrl-D at a shell prompt). A deliberate exit returns the
  remote command's status, so a clean shell exit is 0.

  This client dying does NOT stop the remote process. The pod owns it,
  and a killed client leaves it running with no one attached. For work
  that should survive a disconnect on purpose, use an agent-box, whose
  tmux session is designed to be re-attached (astro box attach).

  Non-interactive runs (--no-tty, or piped stdin) keep the ordinary
  behaviour: Ctrl-C ends the command like any other program in a script.

Examples:
  astro exec --app web -- bash
  astro exec --app web -- python manage.py migrate
  astro exec --app web --pod web-7c9f-abc -c sidecar -- sh
  echo "select 1" | astro exec --app web --no-tty -- psql "$DATABASE_URL"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		if _, err := resolveOrg(cmd, cmd.Context(), client, cfg); err != nil {
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
	var reviewed *execEnvironmentTarget
	if execEnvironment != "" {
		var err error
		reviewed, err = reviewExecEnvironment(ctx, client)
		if err != nil {
			return err
		}
		pod, container = reviewed.PodName, reviewed.Container
		fmt.Fprintf(cmd.ErrOrStderr(), "Exec target: app %s (%s), workload %s, environment %s (%s), cluster %s, namespace %s, pod %s (%s), container %s; UID binding is preflight only; disconnect ends this session.\n", execApp, reviewed.AppID, reviewed.WorkloadID, reviewed.EnvironmentName, reviewed.EnvironmentID, reviewed.ClusterID, reviewed.Namespace, reviewed.PodName, reviewed.PodUID, reviewed.Container)
	}
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
	if reviewed != nil {
		wsURL += "?environmentId=" + url.QueryEscape(reviewed.EnvironmentID)
	}

	header := http.Header{}
	if tok := client.Token(); tok != "" {
		header.Set("Authorization", "Bearer "+tok)
	}
	if org := client.Org(); org != "" {
		header.Set("X-Astrolift-Organization", org)
	}
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return fmt.Errorf("exec unauthorized — run `astro auth login`")
			case http.StatusForbidden:
				// The relay closes before accepting the WebSocket on every
				// rejection, so all of its close codes reach us as one HTTP
				// 403 — "no such target in your org" and "you lack the grant"
				// are indistinguishable from here. Naming a single cause would
				// be a guess, and the wrong guess sends someone auditing RBAC
				// over a typo (or the reverse).
				return fmt.Errorf(
					"exec rejected for %q — either nothing by that name exists in your "+
						"organization, or you lack the permission to exec into it "+
						"(app.exec_pod for an app, agent_box.attach for an agent-box). "+
						"The relay does not distinguish the two", execApp)
			}
		}
		return fmt.Errorf("connecting exec socket (%s): %w", wsURL, err)
	}
	defer func() { _ = conn.Close() }()
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	if reviewed != nil {
		if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			return err
		}
	}

	if len(command) == 0 {
		command = []string{"sh"}
	}
	stdinFd := int(os.Stdin.Fd())
	wantTTY := !execNoTTY && execTerm.IsTerminal(stdinFd)

	var restore func()
	if wantTTY {
		oldState, merr := execTerm.MakeRaw(stdinFd)
		if merr == nil {
			restore = func() { _ = execTerm.Restore(stdinFd, oldState) }
			defer restore()
		} else {
			// Said out loud rather than swallowed. Without raw mode the
			// terminal keeps ISIG, so Ctrl-C becomes a SIGINT that kills this
			// client instead of a 0x03 byte the remote process sees. The
			// SIGINT handler below covers it, but an operator whose Ctrl-C
			// now interrupts the remote rather than the shell they think they
			// are in deserves to know which mode they are in (#72).
			fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: could not put the terminal in raw mode (%v); Ctrl-C will "+
					"interrupt the remote process rather than being passed through\n", merr)
		}
	}

	var sessionID string
	var writeMu sync.Mutex
	writeJSON := func(v interface{}) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		if frame, ok := v.(map[string]interface{}); ok && sessionID != "" && frame["type"] != "open" {
			frame["sessionId"] = sessionID
		}
		return conn.WriteJSON(v)
	}

	openFrame := map[string]interface{}{
		"type":      "open",
		"command":   command,
		"container": container,
		// Ask for a PTY only when we're interactive; piped/non-interactive
		// runs get a plain pipe so output isn't echo-doubled or CRLF-mangled.
		"tty": wantTTY,
	}
	if reviewed != nil {
		openFrame["target"] = reviewed
	}
	if err := writeJSON(openFrame); err != nil {
		return fmt.Errorf("sending open: %w", err)
	}

	var stopResize = func() {}
	var stopInterrupt = func() {}
	defer func() { stopResize(); stopInterrupt() }()
	started := false
	startIO := func() {
		if started {
			return
		}
		started = true
		// Window size: send once + on every SIGWINCH.
		sendResize := func() {
			if !wantTTY {
				return
			}
			cols, rows, gerr := execTerm.GetSize(stdinFd)
			if gerr != nil {
				return
			}
			_ = writeJSON(map[string]interface{}{"type": "resize", "rows": rows, "cols": cols})
		}
		sendResize()
		if wantTTY {
			// SIGWINCH is Unix-only, so the resize watcher lives in
			// platform-tagged files (no-op on Windows). See exec_resize_*.go.
			stopResize = watchResize(sendResize)
		}

		// Ctrl-C, and anything else that sends us an interrupt.
		//
		// With raw mode on, the terminal driver does not raise SIGINT at all and
		// the 0x03 byte reaches the remote through the normal stdin path. But an
		// interrupt can arrive by other routes -- an IDE's stop button, a signal
		// to the process group -- and Go's default disposition is to die. That is
		// exit 130 with the remote's fate unstated, which is what #72 reported
		// from the IDE's exec terminal.
		//
		// So on an interactive session an interrupt is forwarded as 0x03 and the
		// client stays up, matching ssh and kubectl exec: Ctrl-C interrupts what
		// is running remotely, it does not tear down the session. This also makes
		// the raw-mode failure above degrade gracefully rather than fatally.
		//
		// Non-interactive runs keep the default. A piped `astro exec -- cmd` in a
		// script is expected to die on Ctrl-C like any other command.
		if wantTTY {
			interrupts := make(chan os.Signal, 1)
			signal.Notify(interrupts, os.Interrupt)
			stopInterrupt = func() { signal.Stop(interrupts); close(interrupts) }
			go func() {
				for range interrupts {
					_ = writeJSON(map[string]interface{}{"type": "stdin", "data": "\x03"})
				}
			}()
		}

		// stdin → stdin frames (best-effort; ends on EOF/error with a close).
		//
		// Bind the source once. Which reader a session pumps from is a property
		// of the session, not of each read, and re-reading the package var every
		// iteration means a goroutine that outlives runExec is still touching it.
		stdinSrc := execStdin
		go func() {
			buf := make([]byte, 4096)
			for {
				n, rerr := stdinSrc.Read(buf)
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

	}
	if reviewed == nil {
		startIO()
	}

	// Read loop → terminal. Returns the container's exit code as the
	// process exit status so scripts can branch on it.
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	target := "pod " + pod
	if container != "" {
		target += ", container " + container
	}
	// sawOutput separates a command that failed out loud from one that ended
	// with nothing at all, which is also how a command that never started
	// looks: Kubernetes reports that on a channel the relay reads for the
	// exit code and does not forward.
	sawOutput := false
	var pendingExit *int
	finishExit := func(code int) error {
		if restore != nil {
			restore()
		}
		if code != 0 {
			_ = conn.Close()
			// Forwarding the status is the contract; doing it without a word
			// is not. Name where it came from, the way kubectl does.
			if sawOutput {
				fmt.Fprintf(errOut, "exec: command exited with code %d (%s)\n", code, target)
			} else {
				fmt.Fprintf(errOut, "exec: exit code %d and no output from %s. A command that "+
					"cannot start in that container (not installed there, or the container is "+
					"not running) ends the same way; choose another with --container\n",
					code, target)
			}
			execExit(code)
		}
		return nil
	}
	for {
		_, data, rerr := conn.ReadMessage()
		if rerr != nil {
			if reviewed != nil {
				return fmt.Errorf("exec session ended before an exit receipt; last input outcome may be unknown; no reconnect or input replay: %w", rerr)
			}
			// The relay sends an exit frame whenever the command ends. A
			// socket that closes first was refused or broken (a permission
			// recheck on open, an expired session, a failure on the relay's
			// side), and returning nil here reported that as success.
			return fmt.Errorf("exec session in %s ended before the command reported an exit status: %w", target, rerr)
		}
		var frame struct {
			Type        string                 `json:"type"`
			Data        string                 `json:"data"`
			Code        int                    `json:"code"`
			Message     string                 `json:"message"`
			SessionID   string                 `json:"sessionId"`
			Resumable   *bool                  `json:"resumable"`
			Disconnect  string                 `json:"disconnect"`
			InputReplay *bool                  `json:"inputReplay"`
			Target      *execEnvironmentTarget `json:"target"`
		}
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		switch frame.Type {
		case "ready":
			if reviewed != nil {
				if started || frame.SessionID == "" || frame.Resumable == nil || *frame.Resumable || frame.InputReplay == nil || *frame.InputReplay || frame.Disconnect != "END" || frame.Target == nil || !reflect.DeepEqual(*reviewed, *frame.Target) {
					return fmt.Errorf("server did not admit the reviewed target and connection-scoped session; stdin was refused")
				}
				writeMu.Lock()
				sessionID = frame.SessionID
				writeMu.Unlock()
				if err := conn.SetReadDeadline(time.Time{}); err != nil {
					return err
				}
				if pendingExit != nil {
					return finishExit(*pendingExit)
				}
				startIO()
			}
		case "stdout":
			sawOutput = sawOutput || frame.Data != ""
			fmt.Fprint(out, frame.Data)
		case "stderr":
			sawOutput = sawOutput || frame.Data != ""
			fmt.Fprint(errOut, frame.Data)
		case "error":
			if reviewed != nil {
				return fmt.Errorf("exec refused: %s", client.RedactDiagnostic(frame.Message))
			}
			sawOutput = true
			fmt.Fprintln(errOut, "exec error: "+frame.Message)
		case "exit":
			if reviewed != nil && !started {
				code := frame.Code
				pendingExit = &code
				continue
			}
			return finishExit(frame.Code)
		}
	}
}

// resolveExecPod lists the app's pods and returns the exec target: the first
// ready, Running pod (narrowed by --workload) and its primary container.
// When nothing qualifies, the error says where it looked and what it found.
func resolveExecPod(ctx context.Context, client *api.Client) (string, string, error) {
	var resp struct {
		Pods []execPodInfo `json:"astroliftAppPods"`
	}
	if err := client.GraphQL(ctx, astroliftAppPodsQuery,
		map[string]interface{}{"appSlug": execApp}, &resp); err != nil {
		return "", "", fmt.Errorf("listing pods for app %q: %w", execApp, err)
	}

	var candidates []execPodInfo
	for _, p := range resp.Pods {
		if execWorkload != "" && p.Workload != execWorkload {
			continue
		}
		if p.readyAndRunning() {
			return p.Name, p.primaryContainer(), nil
		}
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		// Before reporting an app with no pods, check whether the slug names
		// an agent-box. A box is not a RegisteredApp, so it resolves to no
		// pods here and "no pods" is true about the wrong thing: someone
		// following older docs would read it as their box being broken while
		// it sits there running. One query, only on a path that is already
		// failing.
		if hint := agentBoxVerbHint(ctx, client, execApp); hint != "" {
			return "", "", errors.New(hint)
		}
	}
	// No environmentName: the control plane answers from the app's default
	// environment, which is where a session without --environment opens.
	return "", "", noExecPodError(fmt.Sprintf("app %q, default environment", execApp), resp.Pods, candidates)
}

// noExecPodError explains why no pod qualified: where the lookup looked,
// what the control plane listed there, and the state of every candidate.
// where names the app and environment; the workload filter is added here.
func noExecPodError(where string, listed, candidates []execPodInfo) error {
	if len(listed) == 0 {
		return fmt.Errorf("no pods for %s: the control plane listed none "+
			"(not deployed there, or the control plane cannot reach its cluster)", where)
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no pods in workload %q for %s: the control plane listed %s, all in %s",
			execWorkload, where, podCount(len(listed)), execPodWorkloads(listed))
	}
	if execWorkload != "" {
		where += fmt.Sprintf(", workload %q", execWorkload)
	}
	if execPod != "" {
		return fmt.Errorf("pod %q is not in %s; the control plane listed %s: %s",
			execPod, where, podCount(len(candidates)), describeExecPods(candidates))
	}
	return fmt.Errorf("no ready, Running pod for %s; the control plane listed %s: %s. "+
		"To open a session in one anyway, pass --pod <name>",
		where, podCount(len(candidates)), describeExecPods(candidates))
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
	execCmd.Flags().StringVar(&execEnvironment, "environment", "", "Exact environment GUID; review and admission required, no primary fallback")
	execCmd.Flags().StringVar(&execWorkload, "workload", "", "Narrow pod selection to a workload")
	execCmd.Flags().StringVar(&execPod, "pod", "", "Exact pod name (skips auto-resolution)")
	execCmd.Flags().StringVarP(&execContainer, "container", "c", "", "Container name (multi-container pods)")
	execCmd.Flags().BoolVar(&execNoTTY, "no-tty", false, "Force non-interactive (no TTY) even on a terminal")
	rootCmd.AddCommand(execCmd)
}
