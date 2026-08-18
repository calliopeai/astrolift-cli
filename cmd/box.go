// Package cmd — `astro box ...` : agent-boxes, the warm containers an
// interactive agent session attaches to.
//
// A box is not a task. `astro agent dispatch` starts a batch run that does a
// thing and ends; a box runs nothing in particular and exists so a human or an
// IDE can attach a terminal to it. The pod holds a tmux session open, so the
// agent survives a dropped WebSocket, an IDE restart, or a closed laptop —
// which is the whole reason the platform grew a persistent run mode for it.
//
// `ensure` rather than `create` is the load-bearing choice. The caller is a
// button, pressed whenever someone wants their agent, so the same request must
// return the box they already have rather than a second one on a second node.
// Idempotency is settled control-plane side: the box slug is derived from what
// was asked for, and it is unique among an org's live boxes, so two presses
// racing collide in the database and the loser reads the winner's box.
//
// GraphQL operations (field names per backend/schema.graphql):
//   - ensure → ensureAgentBox(input, orgId) → { ok, errors, data: AstroliftAgentBox }
//   - ls     → agentBoxes(orgId, includeEnded) → [AstroliftAgentBox!]!
//   - rm     → destroyAgentBox(slug) → { ok, errors, data }
//   - attach → agentBox(slug) to poll, then the exec relay
//
// Issue: calliopeai/astrolift-cli#73 (platform half: calliopeai/astrolift#128)
package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// ---- GraphQL operations ----------------------------------------------------

// boxFields is the box projection every command selects. Kept in one place so
// `ensure`, `ls` and `attach` can never disagree about what a box looks like.
const boxFields = `
    slug
    name
    status
    agentSlug
    environmentSpecSlug
    image
    idleTimeoutSeconds
    sessionName
    attachCommand
    namespace
    podName
    lastError
    createdAt
    startedAt`

const ensureBoxMutation = `mutation($input: EnsureAgentBoxInput!, $orgId: ID!) {
  ensureAgentBox(input: $input, orgId: $orgId) {
    ok
    errors { code message field }
    data {` + boxFields + `
    }
  }
}`

const destroyBoxMutation = `mutation($slug: String!) {
  destroyAgentBox(slug: $slug) {
    ok
    errors { code message field }
  }
}`

const listBoxesQuery = `query($orgId: ID!, $includeEnded: Boolean!) {
  agentBoxes(orgId: $orgId, includeEnded: $includeEnded) {` + boxFields + `
  }
}`

const getBoxQuery = `query($slug: String!) {
  agentBox(slug: $slug) {` + boxFields + `
  }
}`

// agentBoxPodsQuery resolves a box's pod. A box's pods used to answer to
// `astroliftAppPods` as well; astrolift-app#1482 removed that on purpose,
// because a second door into them gated on `app.read_logs` outlived its
// reason and leaked box pods into an unrelated workload breakdown. This
// field is gated on `agent_box.attach` — the same grant that authorizes the
// attach itself, so a role that may reach a box may also find it.
const agentBoxPodsQuery = `query($slug: String!) {
  agentBoxPods(slug: $slug) {
    name
    phase
    ready
    containerStatuses { name }
  }
}`

// unknownFieldError reports whether a GraphQL failure is "this server has
// never heard of that field".
//
// Astrolift installs are independently versioned — one DNS zone and database
// each, upgraded on their own schedule — so a released CLI talks to control
// planes both older and newer than the surface it was built against. A server
// rejects the *whole* query on an unknown selection rather than returning a
// partial result, so a new field cannot be probed by inspecting the response:
// it has to be recognised in the error and retried a different way.
func unknownFieldError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cannot query field") ||
		strings.Contains(msg, "unknown field") ||
		strings.Contains(msg, "field \"agentboxpods\"")
}

// ---- response shapes -------------------------------------------------------

// agentBox mirrors the AstroliftAgentBox GraphQL type.
type agentBox struct {
	Slug                string   `json:"slug"`
	Name                string   `json:"name"`
	Status              string   `json:"status"`
	AgentSlug           string   `json:"agentSlug"`
	EnvironmentSpecSlug string   `json:"environmentSpecSlug"`
	Image               string   `json:"image"`
	IdleTimeoutSeconds  int      `json:"idleTimeoutSeconds"`
	SessionName         string   `json:"sessionName"`
	AttachCommand       []string `json:"attachCommand"`
	Namespace           string   `json:"namespace"`
	PodName             string   `json:"podName"`
	LastError           string   `json:"lastError"`
	CreatedAt           string   `json:"createdAt"`
	StartedAt           *string  `json:"startedAt"`
}

type boxMutationResult struct {
	Ok     bool `json:"ok"`
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field"`
	} `json:"errors"`
	Data *agentBox `json:"data"`
}

// boxLiveStatuses are the statuses in which a box may still be holding a node.
// Mirrors AgentBox.LIVE_STATUSES server-side.
var boxLiveStatuses = map[string]bool{
	"pending":      true,
	"provisioning": true,
	"running":      true,
}

// ---- flags -----------------------------------------------------------------

var (
	boxEnsureAgent string
	boxEnsureSpec  string
	boxEnsureName  string
	boxEnsureIdle  string
	boxEnsureWait  bool
	boxEnsureJSON  bool

	boxListAll  bool
	boxListJSON bool

	boxRmYes bool

	boxAttachAgent string
	boxAttachSpec  string
	boxAttachIdle  string
)

// boxWaitPollInterval is how often --wait re-reads the box. A var so tests
// don't sleep.
var boxWaitPollInterval = 3 * time.Second

// boxWaitTimeout bounds --wait. A box that has not come up in this long is
// stuck on an image pull or a missing secret, not slow.
var boxWaitTimeout = 5 * time.Minute

// ---- top-level group -------------------------------------------------------

var boxCmd = &cobra.Command{
	Use:   "box",
	Short: "Manage agent-boxes: warm containers you attach an interactive agent to",
	Long: `An agent-box is a long-lived pod whose purpose is to be attached to.

Unlike "astro agent dispatch", which starts a batch run that ends, a box holds
a tmux session open and waits. That is what lets an agent survive a dropped
connection, an IDE restart, or a closed laptop, and what lets more than one
person watch the same session.

Start with "astro box ensure". It is idempotent: run it again and you attach to
the box you already have rather than paying for a second one.`,
}

// ---- astro box ensure ------------------------------------------------------

var boxEnsureCmd = &cobra.Command{
	Use:   "ensure",
	Short: "Get a warm agent-box, starting one only if you don't have one",
	Long: `Returns the box for this agent, starting it if it isn't already warm.

Idempotent by design: this is what a button calls, so pressing it twice must
attach to the existing box rather than start a rival one. A box that was
idle-reaped is restarted under the same slug, so an address you stored keeps
working.

Name what to run with --agent (a registered agent whose run mode is
persistent), with --env-spec (an environment spec, which is where the image and
the secret packet come from), or both.

--idle-timeout takes a duration ("90m", "4h"), a bare number of seconds, or
"never". It is measured from the last pane activity rather than the last
attach, so an agent working while you are away keeps its box. "never" holds
the node until you destroy it.

Examples:
  astro box ensure --env-spec claude-dev
  astro box ensure --agent claude-box --idle-timeout 4h --wait
  astro box ensure --env-spec claude-dev --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runBoxEnsure(cmd, cmd.Context(), client, cfg)
	},
}

func runBoxEnsure(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) error {
	box, err := ensureBox(cmd, ctx, client, cfg, boxEnsureAgent, boxEnsureSpec, boxEnsureName, boxEnsureIdle)
	if err != nil {
		return err
	}

	if boxEnsureWait {
		box, err = waitForBox(cmd, ctx, client, box)
		if err != nil {
			return err
		}
	}

	if boxEnsureJSON {
		return renderJSON(cmd, box)
	}
	printBox(cmd, box)
	return nil
}

// ensureBox is the ensure call itself, shared by `ensure` and `attach`.
func ensureBox(
	cmd *cobra.Command,
	ctx context.Context,
	client *api.Client,
	cfg *config.Config,
	agent, spec, name, idle string,
) (*agentBox, error) {
	if strings.TrimSpace(agent) == "" && strings.TrimSpace(spec) == "" {
		return nil, fmt.Errorf("a box needs something to run: pass --agent or --env-spec")
	}

	input := map[string]interface{}{}
	if s := strings.TrimSpace(agent); s != "" {
		input["agentSlug"] = s
	}
	if s := strings.TrimSpace(spec); s != "" {
		input["environmentSpecSlug"] = s
	}
	if s := strings.TrimSpace(name); s != "" {
		input["name"] = s
	}
	// Only send idleTimeoutSeconds when the operator actually chose one.
	// Sending a zero we invented would read as "never reap" and quietly pin a
	// node, which is the failure the timeout exists to prevent.
	if strings.TrimSpace(idle) != "" {
		seconds, err := parseIdleTimeout(idle)
		if err != nil {
			return nil, err
		}
		input["idleTimeoutSeconds"] = seconds
	}

	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return nil, err
	}

	ensureCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var resp struct {
		Result boxMutationResult `json:"ensureAgentBox"`
	}
	if err := client.GraphQL(ensureCtx, ensureBoxMutation,
		map[string]interface{}{"input": input, "orgId": org.ID}, &resp); err != nil {
		return nil, fmt.Errorf("ensuring box: %w", err)
	}
	if !resp.Result.Ok {
		return nil, fmt.Errorf("ensure failed: %s", firstMutationError(resp.Result.Errors))
	}
	if resp.Result.Data == nil {
		return nil, fmt.Errorf("ensure succeeded but the server returned no box")
	}
	return resp.Result.Data, nil
}

// parseIdleTimeout accepts a duration ("90m"), bare seconds ("5400"), or the
// explicit opt-out ("never" / "0"). Seconds are accepted because that is what
// the API speaks and what a script is most likely to already hold.
func parseIdleTimeout(raw string) (int, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "never" || v == "0" {
		return 0, nil
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("--idle-timeout cannot be negative; use 'never' to disable reaping")
		}
		return n, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("--idle-timeout %q is not a duration ('90m'), a number of seconds, or 'never'", raw)
	}
	if d < 0 {
		return 0, fmt.Errorf("--idle-timeout cannot be negative; use 'never' to disable reaping")
	}
	return int(d.Seconds()), nil
}

// waitForBox polls until the box is attachable or has settled. Provisioning is
// genuinely slow (image pull), so the caller is told what is happening rather
// than left watching a cursor.
func waitForBox(cmd *cobra.Command, ctx context.Context, client *api.Client, box *agentBox) (*agentBox, error) {
	if box.Status == "running" {
		return box, nil
	}
	out := cmd.ErrOrStderr()
	fmt.Fprintf(out, "Waiting for box %s to come up...\n", box.Slug)

	deadline := time.Now().Add(boxWaitTimeout)
	last := box.Status
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(boxWaitPollInterval):
		}

		fetched, err := getBox(ctx, client, box.Slug)
		if err != nil {
			return nil, err
		}
		// A box that vanished between the ensure and the poll was destroyed
		// out from under us; that is a real answer, not a transient miss.
		if fetched == nil {
			return nil, fmt.Errorf("box %s is gone", box.Slug)
		}
		if fetched.Status != last {
			fmt.Fprintf(out, "  → %s\n", fetched.Status)
			last = fetched.Status
		}
		if fetched.Status == "running" {
			return fetched, nil
		}
		if !boxLiveStatuses[fetched.Status] {
			if fetched.LastError != "" {
				return nil, fmt.Errorf("box %s ended in %q: %s", fetched.Slug, fetched.Status, fetched.LastError)
			}
			return nil, fmt.Errorf("box %s ended in %q before it was attachable", fetched.Slug, fetched.Status)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"timed out waiting for box %s (last status: %s) — check `astro box ls` and the pod's events",
				fetched.Slug, fetched.Status)
		}
	}
}

func getBox(ctx context.Context, client *api.Client, slug string) (*agentBox, error) {
	getCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var resp struct {
		AgentBox *agentBox `json:"agentBox"`
	}
	if err := client.GraphQL(getCtx, getBoxQuery, map[string]interface{}{"slug": slug}, &resp); err != nil {
		return nil, fmt.Errorf("fetching box: %w", err)
	}
	return resp.AgentBox, nil
}

// ---- astro box ls ----------------------------------------------------------

var boxListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List the org's agent-boxes",
	Long: `Lists warm boxes — the ones you can attach to.

Settled boxes (idle-reaped, stopped, failed) are left out by default, because
listing them beside live ones invites attaching to something that no longer
exists. Pass --all to see them, which is how you find out why a box went away.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runBoxList(cmd, cmd.Context(), client, cfg)
	},
}

func runBoxList(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) error {
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}

	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		AgentBoxes []agentBox `json:"agentBoxes"`
	}
	if err := client.GraphQL(listCtx, listBoxesQuery,
		map[string]interface{}{"orgId": org.ID, "includeEnded": boxListAll}, &resp); err != nil {
		return fmt.Errorf("listing boxes: %w", err)
	}

	out := cmd.OutOrStdout()
	if boxListJSON {
		return renderJSON(cmd, resp.AgentBoxes)
	}
	if len(resp.AgentBoxes) == 0 {
		if boxListAll {
			fmt.Fprintln(out, "No boxes found.")
		} else {
			fmt.Fprintln(out, "No warm boxes. Start one with `astro box ensure --env-spec <slug>`.")
		}
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tSTATUS\tAGENT\tIDLE\tSTARTED")
	for _, b := range resp.AgentBoxes {
		agent := b.AgentSlug
		if agent == "" {
			agent = b.EnvironmentSpecSlug
		}
		if agent == "" {
			agent = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			b.Slug, b.Status, agent, formatIdleTimeout(b.IdleTimeoutSeconds), shortTime(b.StartedAt))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d box(es) shown.\n", len(resp.AgentBoxes))
	return nil
}

// formatIdleTimeout renders the reap window, naming the never-reap case rather
// than printing a bare "0s" that reads like "reaps immediately".
func formatIdleTimeout(seconds int) string {
	if seconds == 0 {
		return "never"
	}
	return (time.Duration(seconds) * time.Second).String()
}

// ---- astro box rm ----------------------------------------------------------

var boxRmCmd = &cobra.Command{
	Use:     "rm <slug>",
	Aliases: []string{"destroy"},
	Short:   "Destroy an agent-box now rather than waiting for it to go idle",
	Long: `Tears the box's pod down and retires the row.

Anything running inside the tmux session is killed, so this is the wrong verb
for "I am done for the day" — a box left alone reaps itself once it is idle.
Reach for this when a box is wedged, or when you want the node back now.

Prompts for confirmation unless --yes is given.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runBoxRm(cmd, cmd.Context(), client, args[0])
	},
}

func runBoxRm(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	if !boxRmYes {
		noPrompt, _ := cmd.Root().PersistentFlags().GetBool("no-prompt")
		if !noPrompt {
			fmt.Fprintf(cmd.OutOrStdout(), "Destroy box %s and kill its session? [y/N] ", slug)
			var answer string
			fmt.Fscan(cmd.InOrStdin(), &answer)
			if strings.ToLower(strings.TrimSpace(answer)) != "y" {
				fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
				return nil
			}
		}
	}

	rmCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var resp struct {
		Result boxMutationResult `json:"destroyAgentBox"`
	}
	if err := client.GraphQL(rmCtx, destroyBoxMutation, map[string]interface{}{"slug": slug}, &resp); err != nil {
		return fmt.Errorf("destroying box: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("destroy failed: %s", firstMutationError(resp.Result.Errors))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Box %s destroyed.\n", slug)
	return nil
}

// ---- astro box attach ------------------------------------------------------

var boxAttachCmd = &cobra.Command{
	Use:   "attach [slug]",
	Short: "Ensure a box and attach your terminal to its agent session",
	Long: `The happy path: get a box, wait for it, and join its tmux session.

With a slug, attaches to that box. Without one, ensures a box first from
--agent / --env-spec, so a single command goes from nothing to an attached
agent.

Attaching joins the session rather than starting a new one, so detaching
(Ctrl-B D) leaves the agent running and re-attaching later picks it back up.
Closing the terminal does the same — the session is the pod's, not yours.

Examples:
  astro box attach --env-spec claude-dev
  astro box attach box-claude-dev`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		slug := ""
		if len(args) == 1 {
			slug = args[0]
		}
		return runBoxAttach(cmd, cmd.Context(), client, cfg, slug)
	},
}

func runBoxAttach(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, slug string) error {
	var box *agentBox
	var err error

	if slug != "" {
		box, err = getBox(ctx, client, slug)
		if err != nil {
			return err
		}
		if box == nil {
			return fmt.Errorf("box %s not found (see `astro box ls`)", slug)
		}
	} else {
		box, err = ensureBox(cmd, ctx, client, cfg, boxAttachAgent, boxAttachSpec, "", boxAttachIdle)
		if err != nil {
			return err
		}
	}

	box, err = waitForBox(cmd, ctx, client, box)
	if err != nil {
		return err
	}

	command := box.AttachCommand
	if len(command) == 0 {
		// Older control planes returned no attach command; the session name is
		// still enough to build one, and guessing here beats failing.
		session := box.SessionName
		if session == "" {
			session = "astrolift"
		}
		command = []string{"tmux", "new-session", "-A", "-s", session}
	}

	pod, container, err := resolveBoxPod(ctx, client, box)
	if err != nil {
		return err
	}

	// runExec reads its target from the package-level exec flags. Point them at
	// the box and restore them after, so `box attach` can't leak state into a
	// later `exec` in the same process (the test binary, mainly).
	prevApp, prevPod, prevContainer := execApp, execPod, execContainer
	defer func() { execApp, execPod, execContainer = prevApp, prevPod, prevContainer }()
	execApp, execPod, execContainer = box.Slug, pod, container

	return runExec(cmd, ctx, client, command)
}

// resolveBoxPod finds the pod to dial, tolerating both sides of a control
// plane that may or may not carry astrolift-app#1482.
//
// Three steps, cheapest first:
//
//  1. The row's own `podName`. A server that stamps it (post-#1482) makes this
//     the normal case and costs no query at all.
//  2. `agentBoxPods`, gated on the same grant as the attach itself.
//  3. Nothing — leave the pod blank and let `runExec` resolve it the way it
//     always has, which is what works against a server predating #1482.
//
// Step 3 is the one worth keeping. Before #1482 a box answered to
// `astroliftAppPods`; after it, that door is closed and `agentBoxPods` is the
// only one. A CLI that assumed either shape would break against half the
// installs in the field, and the failure would look like a broken box rather
// than a version difference.
func resolveBoxPod(ctx context.Context, client *api.Client, box *agentBox) (string, string, error) {
	if box.PodName != "" {
		return box.PodName, "", nil
	}

	podCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Pods []execPodInfo `json:"agentBoxPods"`
	}
	if err := client.GraphQL(podCtx, agentBoxPodsQuery,
		map[string]interface{}{"slug": box.Slug}, &resp); err != nil {
		if unknownFieldError(err) {
			// Older install: no such field. Blank pod means runExec falls back
			// to the resolver, which still answers a box slug there.
			return "", "", nil
		}
		return "", "", fmt.Errorf("resolving the box pod: %w", err)
	}

	if len(resp.Pods) == 0 {
		return "", "", fmt.Errorf(
			"box %s is %s but has no pod yet — it may still be starting; check `astro box ls`",
			box.Slug, box.Status)
	}

	pick := resp.Pods[0]
	for _, candidate := range resp.Pods {
		if candidate.Ready && strings.EqualFold(candidate.Phase, "Running") {
			pick = candidate
			break
		}
	}
	container := ""
	if len(pick.ContainerStatuses) > 0 {
		container = pick.ContainerStatuses[0].Name
	}
	return pick.Name, container, nil
}

// ---- output ----------------------------------------------------------------

func printBox(cmd *cobra.Command, box *agentBox) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Box:     %s\n", box.Slug)
	fmt.Fprintf(out, "Status:  %s\n", box.Status)
	if box.AgentSlug != "" {
		fmt.Fprintf(out, "Agent:   %s\n", box.AgentSlug)
	}
	if box.EnvironmentSpecSlug != "" {
		fmt.Fprintf(out, "Spec:    %s\n", box.EnvironmentSpecSlug)
	}
	fmt.Fprintf(out, "Idle:    %s\n", formatIdleTimeout(box.IdleTimeoutSeconds))
	if box.LastError != "" {
		fmt.Fprintf(out, "Error:   %s\n", box.LastError)
	}
	if !boxLiveStatuses[box.Status] {
		return
	}
	fmt.Fprintf(out, "\nAttach with:\n  astro box attach %s\n", box.Slug)
}

// ---- init ------------------------------------------------------------------

func init() {
	boxEnsureCmd.Flags().StringVar(&boxEnsureAgent, "agent", "", "Registered agent slug (run mode must be persistent)")
	boxEnsureCmd.Flags().StringVar(&boxEnsureSpec, "env-spec", "", "Agent environment spec slug (image + secret packet)")
	boxEnsureCmd.Flags().StringVar(&boxEnsureName, "name", "", "Human-readable name (defaults to the agent or spec name)")
	boxEnsureCmd.Flags().StringVar(&boxEnsureIdle, "idle-timeout", "", "Reap after this much inactivity: 90m, seconds, or never")
	boxEnsureCmd.Flags().BoolVar(&boxEnsureWait, "wait", false, "Block until the box is attachable")
	boxEnsureCmd.Flags().BoolVar(&boxEnsureJSON, "json", false, "Output the box record as JSON")

	boxListCmd.Flags().BoolVar(&boxListAll, "all", false, "Include settled boxes (reaped, stopped, failed)")
	boxListCmd.Flags().BoolVar(&boxListJSON, "json", false, "Output as JSON")

	boxRmCmd.Flags().BoolVarP(&boxRmYes, "yes", "y", false, "Skip confirmation prompt")

	boxAttachCmd.Flags().StringVar(&boxAttachAgent, "agent", "", "Registered agent slug, when ensuring a box to attach to")
	boxAttachCmd.Flags().StringVar(&boxAttachSpec, "env-spec", "", "Environment spec slug, when ensuring a box to attach to")
	boxAttachCmd.Flags().StringVar(&boxAttachIdle, "idle-timeout", "", "Reap after this much inactivity: 90m, seconds, or never")

	boxCmd.AddCommand(boxEnsureCmd, boxListCmd, boxRmCmd, boxAttachCmd)
	rootCmd.AddCommand(boxCmd)
}
