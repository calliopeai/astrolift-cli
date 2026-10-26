// Package cmd -- `astro brain ...`: semantic layers (Mimwell brains) end to
// end (astrolift-app#2424).
//
// A semantic layer is an app with [app] kind = "mimwell". Every command
// resolves it by app slug; one the caller cannot read, an archived one and an
// app that is not a brain all read the same ("not found").
//
//   - list       -> astroliftBrains(search)
//   - show       -> astroliftBrain(slug)
//   - status     -> astroliftBrainStatus(slug, environmentName)
//   - connect    -> astroliftBrain + issueBrainReadToken (15-minute token)
//   - create     -> createBrain, then astroliftBrainCreation with --wait
//   - share      -> shareBrain (viewer = VIEWER, editor = DEPLOYER; --agent viewer only, #2449)
//   - unshare    -> unshareBrain
//   - host-app   -> hostAppOnBrain (+ startDeployment with --deploy)
//   - rebuild    -> rebuildBrain
//   - substrate  -> updateBrainSubstrate
//   - delete     -> archiveBrain
//
// A server without these operations answers with a schema mismatch; every
// command turns that into one line naming what the server needs.
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const brainFields = `id slug name kind team project
    environments { name url }
    sharedWith { team access }`

const brainSubstrateFields = `authority blobs vectors graph cell persistence replicas image
    adopted adoptedStore graduationNeeded
    resources { kind name environment state detail }`

const brainCreationFields = `id slug state repo templateRef commitSha deploymentId
    app { ` + brainFields + ` }
    error { code message field }`

const (
	brainListQuery = `query($search: String) {
  astroliftBrains(search: $search) { ` + brainFields + ` }
}`
	brainShowQuery = `query($slug: String!) {
  astroliftBrain(slug: $slug) {
    ` + brainFields + `
    hostedApps { slug environment }
    substrate { ` + brainSubstrateFields + ` }
    agentShares { agent app grantedAt }
  }
}`
	// brainShowLegacyQuery is brainShowQuery for a server older than platform
	// #2449, which has no agent shares.
	brainShowLegacyQuery = `query($slug: String!) {
  astroliftBrain(slug: $slug) {
    ` + brainFields + `
    hostedApps { slug environment }
    substrate { ` + brainSubstrateFields + ` }
  }
}`
	brainStatusQuery = `query($slug: String!, $environmentName: String!) {
  astroliftBrainStatus(slug: $slug, environmentName: $environmentName) {
    slug environmentName url served deployedCommit behind compiledAt warnings
    deployment { id status imageTag commitSha createdAt }
    sourceHead { branch commitSha committedAt }
    lastIngest { status conclusion url createdAt }
    substrate { ` + brainSubstrateFields + ` }
  }
}`
	brainCreationQuery = `query($id: GUID!) {
  astroliftBrainCreation(id: $id) { ` + brainCreationFields + ` }
}`
	brainConnectMutation = `mutation($slug: String!) {
  issueBrainReadToken(slug: $slug) {
    ok errors { code message field }
    data { id slug endpoints header token expiresAt }
  }
}`
	brainCreateMutation = `mutation($input: CreateBrainInput!) {
  createBrain(input: $input) {
    ok errors { code message field approvalRequestId }
    data { ` + brainCreationFields + ` }
  }
}`
	brainSubstrateMutation = `mutation($input: UpdateBrainSubstrateInput!) {
  updateBrainSubstrate(input: $input) {
    ok errors { code message field }
    data { ` + brainFields + ` substrate { ` + brainSubstrateFields + ` } }
  }
}`
	brainShareMutation = `mutation($input: ShareBrainInput!) {
  shareBrain(input: $input) {
    ok errors { code message field approvalRequestId }
    data { ` + brainFields + ` }
  }
}`
	brainUnshareMutation = `mutation($input: UnshareBrainInput!) {
  unshareBrain(input: $input) {
    ok errors { code message field }
    data { ` + brainFields + ` }
  }
}`
	brainShareAgentMutation = `mutation($input: ShareBrainInput!) {
  shareBrain(input: $input) {
    ok errors { code message field approvalRequestId }
    data { ` + brainFields + ` agentShares { agent app grantedAt } }
  }
}`
	brainUnshareAgentMutation = `mutation($input: UnshareBrainInput!) {
  unshareBrain(input: $input) {
    ok errors { code message field }
    data { ` + brainFields + ` agentShares { agent app grantedAt } }
  }
}`
	brainHostAppMutation = `mutation($input: HostAppOnBrainInput!) {
  hostAppOnBrain(input: $input) {
    ok errors { code message field }
    data { brainSlug template environmentName envKeys app { id slug name } }
  }
}`
	brainRebuildMutation = `mutation($input: RebuildBrainInput!) {
  rebuildBrain(input: $input) {
    ok errors { code message field approvalRequestId }
    data { slug mode deploymentId runUrl ingestRunUrl rebuilds }
  }
}`
	brainDeleteMutation = `mutation($input: ArchiveBrainInput!) {
  archiveBrain(input: $input) {
    ok errors { code message field approvalRequestId }
    data { ` + brainFields + ` }
  }
}`
	brainTeamsQuery = `query($search: String) {
  astroliftTeamsPage(search: $search, limit: 50) { items { id slug name } }
}`
	brainConnectionsQuery = `query($search: String) {
  astroliftSourceConnectionsPage(search: $search, limit: 50) { items { id kind name displayName accountLogin isActive } }
}`
)

// ---- response shapes --------------------------------------------------------

type brainEnvironment struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type brainShare struct {
	Team   string `json:"team"`
	Access string `json:"access"`
}

// brainAgentShare is an agent the semantic layer is shared with (#2449),
// viewer only; app is the agent's app.
type brainAgentShare struct {
	Agent     string `json:"agent"`
	App       string `json:"app"`
	GrantedAt string `json:"grantedAt"`
}

type brainHostedApp struct {
	Slug        string `json:"slug"`
	Environment string `json:"environment"`
}

type brainResource struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	State       string `json:"state"`
	Detail      string `json:"detail"`
}

type brainSubstrate struct {
	Authority        string          `json:"authority"`
	Blobs            string          `json:"blobs"`
	Vectors          string          `json:"vectors"`
	Graph            string          `json:"graph"`
	Cell             string          `json:"cell"`
	Persistence      []string        `json:"persistence"`
	Replicas         int             `json:"replicas"`
	Image            string          `json:"image"`
	Adopted          *bool           `json:"adopted"`
	AdoptedStore     string          `json:"adoptedStore"`
	GraduationNeeded *bool           `json:"graduationNeeded"`
	Resources        []brainResource `json:"resources"`
}

type brain struct {
	ID           string             `json:"id"`
	Slug         string             `json:"slug"`
	Name         string             `json:"name"`
	Kind         string             `json:"kind"`
	Team         string             `json:"team"`
	Project      string             `json:"project"`
	Environments []brainEnvironment `json:"environments"`
	SharedWith   []brainShare       `json:"sharedWith"`
	HostedApps   []brainHostedApp   `json:"hostedApps,omitempty"`
	Substrate    *brainSubstrate    `json:"substrate,omitempty"`
	// AgentShares is nil where the server does not report them (lists, and
	// servers older than platform #2449).
	AgentShares *[]brainAgentShare `json:"agentShares,omitempty"`
}

type brainStatus struct {
	Slug            string `json:"slug"`
	EnvironmentName string `json:"environmentName"`
	URL             string `json:"url"`
	Served          bool   `json:"served"`
	Deployment      *struct {
		ID        string  `json:"id"`
		Status    string  `json:"status"`
		ImageTag  string  `json:"imageTag"`
		CommitSHA string  `json:"commitSha"`
		CreatedAt *string `json:"createdAt"`
	} `json:"deployment"`
	DeployedCommit string `json:"deployedCommit"`
	SourceHead     *struct {
		Branch      string  `json:"branch"`
		CommitSHA   string  `json:"commitSha"`
		CommittedAt *string `json:"committedAt"`
	} `json:"sourceHead"`
	Behind     *bool   `json:"behind"`
	CompiledAt *string `json:"compiledAt"`
	LastIngest *struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		URL        string `json:"url"`
		CreatedAt  string `json:"createdAt"`
	} `json:"lastIngest"`
	Substrate brainSubstrate `json:"substrate"`
	Warnings  []string       `json:"warnings"`
}

type brainConnection struct {
	ID        string   `json:"id"`
	Slug      string   `json:"slug"`
	Endpoints []string `json:"endpoints"`
	Header    string   `json:"header"`
	Token     string   `json:"token"`
	ExpiresAt string   `json:"expiresAt"`
}

type brainCreation struct {
	ID           string         `json:"id"`
	Slug         string         `json:"slug"`
	State        string         `json:"state"`
	Repo         string         `json:"repo"`
	TemplateRef  string         `json:"templateRef"`
	CommitSHA    *string        `json:"commitSha"`
	DeploymentID *string        `json:"deploymentId"`
	App          *brain         `json:"app"`
	Error        *mutationError `json:"error"`
}

type brainRebuild struct {
	Slug         string   `json:"slug"`
	Mode         string   `json:"mode"`
	DeploymentID *string  `json:"deploymentId"`
	RunURL       *string  `json:"runUrl"`
	IngestRunURL *string  `json:"ingestRunUrl"`
	Rebuilds     []string `json:"rebuilds"`
}

type brainHostedAppResult struct {
	BrainSlug       string   `json:"brainSlug"`
	Template        string   `json:"template"`
	EnvironmentName string   `json:"environmentName"`
	EnvKeys         []string `json:"envKeys"`
	App             struct {
		ID        string  `json:"id"`
		Slug      string  `json:"slug"`
		Name      string  `json:"name"`
		ExpiresAt *string `json:"expiresAt"`
	} `json:"app"`
}

// brainMutationError is the MutationError subset the brain writes read; an
// approval rule answers with the request it opened.
type brainMutationError struct {
	Code              string  `json:"code"`
	Message           string  `json:"message"`
	Field             string  `json:"field"`
	ApprovalRequestID *string `json:"approvalRequestId"`
}

type brainResult[T any] struct {
	OK     bool                 `json:"ok"`
	Errors []brainMutationError `json:"errors"`
	Data   *T                   `json:"data"`
}

// ---- flags ------------------------------------------------------------------

var (
	brainSearch        string
	brainEnv           string
	brainConnectEnv    string
	brainFormat        string
	brainWrite         string
	brainConnectionSel string
	brainRepo          string
	brainName          string
	brainVisibility    string
	brainTemplateRef   string
	brainNoDeploy      bool
	brainWait          bool
	brainYes           bool
	brainAccess        string
	brainAgent         string
	brainIngest        bool
	brainHostImage     string
	brainHostName      string
	brainHostSlug      string
	brainHostPort      int
	brainHostDeploy    bool
	brainAuthority     string
	brainBlobs         string
	brainVectors       string
	brainGraph         string
	brainPersistence   []string
	brainReplicas      int
	brainImage         string
	brainPort          int

	brainPollInterval = 5 * time.Second
	brainWaitTimeout  = 30 * time.Minute
)

// ---- helpers ----------------------------------------------------------------

// brainGraphQL sends one brain operation. A server that does not know it
// (older than the operation) is named in one line instead of a GraphQL error.
func brainGraphQL(ctx context.Context, client *api.Client, doc string, vars map[string]interface{}, out interface{}, what string) error {
	if err := client.GraphQL(ctx, doc, vars, out); err != nil {
		if errors.Is(err, api.ErrSchemaMismatch) {
			return fmt.Errorf("%s: this server does not offer semantic layer management; it needs an Astrolift control plane with platform #2424", what)
		}
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

func brainFailure(what string, errs []brainMutationError) error {
	if len(errs) == 0 {
		return fmt.Errorf("%s failed: unknown error", what)
	}
	e := errs[0]
	msg := e.Message
	if e.Field != "" {
		msg = e.Field + ": " + msg
	}
	if e.Code != "" {
		msg = e.Code + ": " + msg
	}
	if e.ApprovalRequestID != nil && *e.ApprovalRequestID != "" {
		msg += fmt.Sprintf(" (approval request %s; run it again once it is approved)", *e.ApprovalRequestID)
	}
	return fmt.Errorf("%s failed: %s", what, msg)
}

func brainNotFound(slug string) error {
	return fmt.Errorf("semantic layer %q not found (it does not exist, is archived, or you cannot read it)", slug)
}

func fetchBrain(ctx context.Context, client *api.Client, slug string) (*brain, error) {
	var resp struct {
		Brain *brain `json:"astroliftBrain"`
	}
	vars := map[string]interface{}{"slug": slug}
	err := client.GraphQL(ctx, brainShowQuery, vars, &resp)
	if errors.Is(err, api.ErrSchemaMismatch) {
		// Older than platform #2449: no agent shares; AgentShares stays nil.
		err = brainGraphQL(ctx, client, brainShowLegacyQuery, vars, &resp, "reading "+slug)
	} else if err != nil {
		err = fmt.Errorf("reading %s: %w", slug, err)
	}
	if err != nil {
		return nil, err
	}
	if resp.Brain == nil {
		return nil, brainNotFound(slug)
	}
	return resp.Brain, nil
}

// brainConfirm asks before a share or delete. --yes skips it; with prompts
// disabled --yes is required.
func brainConfirm(cmd *cobra.Command, question string) (bool, error) {
	if brainYes {
		return true, nil
	}
	noPrompt, _ := cmd.Flags().GetBool("no-prompt")
	if !noPrompt {
		noPrompt, _ = cmd.Root().PersistentFlags().GetBool("no-prompt")
	}
	if noPrompt {
		return false, fmt.Errorf("pass --yes to %s when prompts are disabled (nothing was changed)", cmd.Name())
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", question)
	var answer string
	_, _ = fmt.Fscan(cmd.InOrStdin(), &answer)
	if !strings.EqualFold(strings.TrimSpace(answer), "y") {
		fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
		return false, nil
	}
	return true, nil
}

func stringFlag(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return strings.TrimSpace(v)
}

// brainEnum turns a flag value such as "sqlite-volume" into its GraphQL enum
// (SQLITE_VOLUME), refusing anything outside allowed.
func brainEnum(flag, value string, allowed ...string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	for _, a := range allowed {
		if v == a {
			return strings.ToUpper(strings.ReplaceAll(a, "-", "_")), nil
		}
	}
	return "", fmt.Errorf("--%s must be one of %s", flag, strings.Join(allowed, ", "))
}

var (
	brainAuthorities  = []string{"files", "sqlite", "postgres", "mysql", "mongo", "d1"}
	brainBlobStores   = []string{"fs", "db-blob", "s3", "azure-blob"}
	brainVectorStores = []string{"local", "in-db", "edge-db", "external"}
	brainGraphStores  = []string{"edges-table", "age"}
	brainPersistences = []string{"none", "sqlite-volume", "efs", "postgres", "s3", "d1", "kv"}
)

// brainSubstrateFlagsSet reports whether any substrate flag was given.
func brainSubstrateFlagsSet(cmd *cobra.Command) bool {
	for _, name := range []string{"authority", "blobs", "vectors", "graph", "persistence", "replicas", "image", "port"} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

// brainSubstrateInput builds a BrainSubstrateInput from the flags, starting
// from current (the brain's applied cell) so a flag left out keeps its value.
func brainSubstrateInput(cmd *cobra.Command, current *brainSubstrate) (map[string]interface{}, error) {
	pick := func(flag, flagValue, currentValue string, allowed []string) (string, error) {
		v := currentValue
		if cmd.Flags().Changed(flag) || v == "" {
			v = flagValue
		}
		return brainEnum(flag, v, allowed...)
	}
	cur := brainSubstrate{Authority: "files", Blobs: "fs", Vectors: "local", Graph: "edges-table", Replicas: 1}
	if current != nil {
		cur = *current
	}
	input := map[string]interface{}{}
	var err error
	if input["authority"], err = pick("authority", brainAuthority, cur.Authority, brainAuthorities); err != nil {
		return nil, err
	}
	if input["blobs"], err = pick("blobs", brainBlobs, cur.Blobs, brainBlobStores); err != nil {
		return nil, err
	}
	if input["vectors"], err = pick("vectors", brainVectors, cur.Vectors, brainVectorStores); err != nil {
		return nil, err
	}
	if input["graph"], err = pick("graph", brainGraph, cur.Graph, brainGraphStores); err != nil {
		return nil, err
	}
	persistence := cur.Persistence
	if cmd.Flags().Changed("persistence") {
		persistence = brainPersistence
	}
	stores := []string{}
	for _, p := range persistence {
		e, err := brainEnum("persistence", p, brainPersistences...)
		if err != nil {
			return nil, err
		}
		stores = append(stores, e)
	}
	input["persistence"] = stores
	replicas := cur.Replicas
	if cmd.Flags().Changed("replicas") {
		replicas = brainReplicas
	}
	if replicas < 1 {
		return nil, errors.New("--replicas must be at least 1")
	}
	input["replicas"] = replicas
	if cmd.Flags().Changed("image") {
		input["image"] = strings.TrimSpace(brainImage)
	}
	if cmd.Flags().Changed("port") {
		input["port"] = brainPort
	}
	return input, nil
}

func brainRunE(run func(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, cfg, _, err := loadActiveClient(ctx, boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		if _, err := resolveOrg(cmd, ctx, client, cfg); err != nil {
			return err
		}
		return run(cmd, ctx, client, cfg, args)
	}
}

func renderBrainSubstrate(out *tabwriter.Writer, s *brainSubstrate) {
	if s == nil {
		return
	}
	fmt.Fprintf(out, "Cell:\t%s (authority %s, blobs %s, vectors %s, graph %s)\n", s.Cell, s.Authority, s.Blobs, s.Vectors, s.Graph)
	fmt.Fprintf(out, "Persistence:\t%s\n", dashIfEmpty(strings.Join(s.Persistence, ", ")))
	fmt.Fprintf(out, "Replicas:\t%d\n", s.Replicas)
	if s.Image != "" {
		fmt.Fprintf(out, "Image:\t%s\n", s.Image)
	}
	if s.Adopted != nil {
		adopted := yesNo(*s.Adopted)
		if *s.Adopted && s.AdoptedStore != "" {
			adopted += " (" + s.AdoptedStore + ")"
		}
		fmt.Fprintf(out, "Store adopted:\t%s\n", adopted)
	}
	if s.GraduationNeeded != nil && *s.GraduationNeeded {
		fmt.Fprintln(out, "Graduation:\tneeded (see astro brain substrate)")
	}
	for _, r := range s.Resources {
		fmt.Fprintf(out, "Resource:\t%s %s [%s] %s %s\n", r.Kind, r.Name, r.Environment, r.State, r.Detail)
	}
}

func renderBrain(cmd *cobra.Command, b *brain) error {
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, b)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Semantic layer:\t%s (%s)\n", b.Slug, b.Name)
	fmt.Fprintf(w, "Team:\t%s\n", b.Team)
	fmt.Fprintf(w, "Project:\t%s\n", b.Project)
	if len(b.Environments) == 0 {
		fmt.Fprintln(w, "Environments:\tnone served yet")
	}
	for _, e := range b.Environments {
		fmt.Fprintf(w, "Environment:\t%s %s\n", e.Name, dashIfEmpty(e.URL))
	}
	for _, s := range b.SharedWith {
		fmt.Fprintf(w, "Shared with:\t%s (%s)\n", s.Team, brainAccessLabel(s.Access))
	}
	if b.AgentShares != nil {
		for _, a := range *b.AgentShares {
			fmt.Fprintf(w, "Shared with agent:\t%s (viewer, app %s)\n", a.Agent, a.App)
		}
	}
	for _, h := range b.HostedApps {
		fmt.Fprintf(w, "Hosted app:\t%s [%s]\n", h.Slug, h.Environment)
	}
	renderBrainSubstrate(w, b.Substrate)
	return w.Flush()
}

// brainAccessLabel names a share the way the CLI takes it: deployer is editor.
func brainAccessLabel(access string) string {
	if strings.EqualFold(access, "deployer") {
		return "editor"
	}
	return strings.ToLower(access)
}

// ---- list / show / status ---------------------------------------------------

func runBrainList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	var resp struct {
		Brains []brain `json:"astroliftBrains"`
	}
	vars := map[string]interface{}{"search": nil}
	if s := strings.TrimSpace(brainSearch); s != "" {
		vars["search"] = s
	}
	if err := brainGraphQL(ctx, client, brainListQuery, vars, &resp, "listing semantic layers"); err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		if resp.Brains == nil {
			resp.Brains = []brain{}
		}
		return renderJSON(cmd, resp.Brains)
	}
	if len(resp.Brains) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No semantic layers you can read.")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tNAME\tTEAM\tPROJECT\tENVIRONMENTS\tSHARED WITH")
	for _, b := range resp.Brains {
		envs := make([]string, 0, len(b.Environments))
		for _, e := range b.Environments {
			envs = append(envs, e.Name)
		}
		shares := make([]string, 0, len(b.SharedWith))
		for _, s := range b.SharedWith {
			shares = append(shares, s.Team+":"+brainAccessLabel(s.Access))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", b.Slug, b.Name, b.Team, b.Project,
			dashIfEmpty(strings.Join(envs, ",")), dashIfEmpty(strings.Join(shares, ",")))
	}
	return w.Flush()
}

func runBrainShow(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	b, err := fetchBrain(ctx, client, slug)
	if err != nil {
		return err
	}
	return renderBrain(cmd, b)
}

func runBrainStatus(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	var resp struct {
		Status *brainStatus `json:"astroliftBrainStatus"`
	}
	vars := map[string]interface{}{"slug": slug, "environmentName": brainEnv}
	if err := brainGraphQL(ctx, client, brainStatusQuery, vars, &resp, "reading the status of "+slug); err != nil {
		return err
	}
	s := resp.Status
	if s == nil {
		return brainNotFound(slug)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, s)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Semantic layer:\t%s [%s]\n", s.Slug, s.EnvironmentName)
	fmt.Fprintf(w, "URL:\t%s\n", dashIfEmpty(s.URL))
	fmt.Fprintf(w, "Served:\t%s\n", yesNo(s.Served))
	if d := s.Deployment; d != nil {
		fmt.Fprintf(w, "Deployment:\t%s %s (image %s, commit %s)\n", d.ID, d.Status, dashIfEmpty(d.ImageTag), dashIfEmpty(shortSHA(d.CommitSHA)))
	} else {
		fmt.Fprintln(w, "Deployment:\tnone")
	}
	fmt.Fprintf(w, "Running commit:\t%s\n", dashIfEmpty(shortSHA(s.DeployedCommit)))
	if h := s.SourceHead; h != nil {
		fmt.Fprintf(w, "Branch head:\t%s %s\n", h.Branch, shortSHA(h.CommitSHA))
	}
	switch {
	case s.Behind == nil:
		fmt.Fprintln(w, "Up to date:\tunknown")
	case *s.Behind:
		fmt.Fprintln(w, "Up to date:\tno, the branch head is not running (astro brain rebuild)")
	default:
		fmt.Fprintln(w, "Up to date:\tyes")
	}
	if s.CompiledAt != nil {
		fmt.Fprintf(w, "Compiled:\t%s\n", *s.CompiledAt)
	} else {
		fmt.Fprintln(w, "Compiled:\tnever (no app/brain-manifest.json at the running commit)")
	}
	if i := s.LastIngest; i != nil {
		fmt.Fprintf(w, "Last ingest:\t%s %s %s %s\n", i.CreatedAt, i.Status, dashIfEmpty(i.Conclusion), i.URL)
	} else {
		fmt.Fprintln(w, "Last ingest:\tnone (no knowledge-refresh.yml run)")
	}
	renderBrainSubstrate(w, &s.Substrate)
	for _, warning := range s.Warnings {
		fmt.Fprintf(w, "Warning:\t%s\n", warning)
	}
	return w.Flush()
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// ---- connect ----------------------------------------------------------------

// brainConnectOutput is what connect prints with --json.
type brainConnectOutput struct {
	Slug        string `json:"slug"`
	Environment string `json:"environment"`
	URL         string `json:"url"`
	Header      string `json:"header"`
	Token       string `json:"token"`
	ExpiresAt   string `json:"expiresAt"`
}

// brainEndpoint picks the environment to connect to: --env, else production,
// else the only served one; and its MCP URL from the issued endpoints.
func brainEndpoint(b *brain, conn *brainConnection, env string) (string, string, error) {
	served := []brainEnvironment{}
	for _, e := range b.Environments {
		if strings.TrimSpace(e.URL) != "" {
			served = append(served, e)
		}
	}
	if len(served) == 0 {
		return "", "", fmt.Errorf("%s is not served in any environment yet; deploy it first", b.Slug)
	}
	var chosen *brainEnvironment
	for i, e := range served {
		if (env != "" && e.Name == env) || (env == "" && e.Name == "production") {
			chosen = &served[i]
		}
	}
	if chosen == nil && env == "" && len(served) == 1 {
		chosen = &served[0]
	}
	if chosen == nil {
		names := make([]string, 0, len(served))
		for _, e := range served {
			names = append(names, e.Name)
		}
		if env == "" {
			return "", "", fmt.Errorf("%s is served in %s; pick one with --env", b.Slug, strings.Join(names, ", "))
		}
		return "", "", fmt.Errorf("%s is not served in %q (served: %s)", b.Slug, env, strings.Join(names, ", "))
	}
	// The server issues each environment's base URL; a brain serves MCP at
	// /mcp on it, as the web console's brainMcpUrl does.
	want := strings.TrimRight(chosen.URL, "/")
	for _, endpoint := range conn.Endpoints {
		if strings.TrimRight(endpoint, "/") == want {
			return chosen.Name, want + "/mcp", nil
		}
	}
	return "", "", fmt.Errorf("the server issued no MCP endpoint for %s in %s", b.Slug, chosen.Name)
}

// brainClientConfig renders the connection for a client.
func brainClientConfig(format, slug, url, header, token string) (string, error) {
	server := map[string]interface{}{
		"type":    "http",
		"url":     url,
		"headers": map[string]string{header: token},
	}
	switch format {
	case "mcp":
		data, err := json.MarshalIndent(map[string]interface{}{"mcpServers": map[string]interface{}{slug: server}}, "", "  ")
		return string(data) + "\n", err
	case "claude":
		data, err := json.Marshal(server)
		return fmt.Sprintf("claude mcp add-json %s '%s'\n", slug, data), err
	case "env":
		return fmt.Sprintf("BRAIN_MCP_URL=%s\nBRAIN_TOKEN_HEADER=%s\nBRAIN_TOKEN=%s\n", url, header, token), nil
	}
	return "", fmt.Errorf("--format must be mcp, claude or env")
}

func runBrainConnect(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	format := strings.ToLower(strings.TrimSpace(brainFormat))
	if _, err := brainClientConfig(format, "", "", "", ""); err != nil {
		return err
	}
	b, err := fetchBrain(ctx, client, slug)
	if err != nil {
		return err
	}
	var resp struct {
		Result brainResult[brainConnection] `json:"issueBrainReadToken"`
	}
	if err := brainGraphQL(ctx, client, brainConnectMutation, map[string]interface{}{"slug": slug}, &resp, "issuing a read token for "+slug); err != nil {
		return err
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return brainFailure("issuing a read token for "+slug, resp.Result.Errors)
	}
	conn := resp.Result.Data
	env, endpoint, err := brainEndpoint(b, conn, strings.TrimSpace(brainConnectEnv))
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, brainConnectOutput{Slug: slug, Environment: env, URL: endpoint, Header: conn.Header, Token: conn.Token, ExpiresAt: conn.ExpiresAt})
	}
	text, err := brainClientConfig(format, slug, endpoint, conn.Header, conn.Token)
	if err != nil {
		return err
	}
	if path := strings.TrimSpace(brainWrite); path != "" {
		if err := privatefile.Write(path, []byte(text)); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (mode 0600). The token expires at %s; run this again to refresh it.\n", path, conn.ExpiresAt)
		return nil
	}
	fmt.Fprint(cmd.OutOrStdout(), text)
	fmt.Fprintf(cmd.ErrOrStderr(), "The token expires at %s; run this again to refresh it.\n", conn.ExpiresAt)
	return nil
}

// ---- create -----------------------------------------------------------------

func resolveBrainTeamID(ctx context.Context, client *api.Client, slug string) (string, error) {
	var resp struct {
		Page struct {
			Items []teamRef `json:"items"`
		} `json:"astroliftTeamsPage"`
	}
	if err := brainGraphQL(ctx, client, brainTeamsQuery, map[string]interface{}{"search": slug}, &resp, "resolving team "+slug); err != nil {
		return "", err
	}
	for _, t := range resp.Page.Items {
		if t.Slug == slug || t.ID == slug {
			return t.ID, nil
		}
	}
	return "", fmt.Errorf("team %q not found in this organization", slug)
}

// resolveBrainConnection takes a source connection id as given, or finds the
// one active connection whose name, display name or account matches.
func resolveBrainConnection(ctx context.Context, client *api.Client, selector string) (string, error) {
	if _, err := uuid.Parse(selector); err == nil {
		return selector, nil
	}
	var resp struct {
		Page struct {
			Items []struct {
				ID           string `json:"id"`
				Kind         string `json:"kind"`
				Name         string `json:"name"`
				DisplayName  string `json:"displayName"`
				AccountLogin string `json:"accountLogin"`
				IsActive     bool   `json:"isActive"`
			} `json:"items"`
		} `json:"astroliftSourceConnectionsPage"`
	}
	if err := brainGraphQL(ctx, client, brainConnectionsQuery, map[string]interface{}{"search": selector}, &resp, "resolving source connection "+selector); err != nil {
		return "", err
	}
	var matches []string
	for _, c := range resp.Page.Items {
		if c.IsActive && (c.Name == selector || c.DisplayName == selector || strings.EqualFold(c.AccountLogin, selector)) {
			matches = append(matches, c.ID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no active source connection %q (see astro scm list)", selector)
	}
	return "", fmt.Errorf("%d source connections match %q; pass its id from astro scm list", len(matches), selector)
}

func runBrainCreate(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, slug string) error {
	if brainConnectionSel == "" || strings.TrimSpace(brainRepo) == "" {
		return errors.New("--connection and --repo are required")
	}
	visibility, err := brainEnum("visibility", brainVisibility, "private", "internal", "public")
	if err != nil {
		return err
	}
	project, err := resolveProjectForResource(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	connectionID, err := resolveBrainConnection(ctx, client, strings.TrimSpace(brainConnectionSel))
	if err != nil {
		return err
	}
	input := map[string]interface{}{
		"projectId":          project.ID,
		"slug":               slug,
		"sourceConnectionId": connectionID,
		"repo":               strings.TrimSpace(brainRepo),
		"visibility":         visibility,
		"environmentName":    brainEnv,
		"deploy":             !brainNoDeploy,
	}
	if team := stringFlag(cmd, "team"); team != "" {
		teamID, err := resolveBrainTeamID(ctx, client, team)
		if err != nil {
			return err
		}
		input["teamId"] = teamID
	}
	if n := strings.TrimSpace(brainName); n != "" {
		input["name"] = n
	}
	if ref := strings.TrimSpace(brainTemplateRef); ref != "" {
		input["templateRef"] = ref
	}
	if brainSubstrateFlagsSet(cmd) {
		substrate, err := brainSubstrateInput(cmd, nil)
		if err != nil {
			return err
		}
		input["substrate"] = substrate
	}
	var resp struct {
		Result brainResult[brainCreation] `json:"createBrain"`
	}
	if err := brainGraphQL(ctx, client, brainCreateMutation, map[string]interface{}{"input": input}, &resp, "creating "+slug); err != nil {
		return err
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return brainFailure("creating "+slug, resp.Result.Errors)
	}
	creation := resp.Result.Data
	if !brainWait {
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, creation)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Creating semantic layer %s in %s (%s).\nCreation: %s\nFollow it with --wait, or run astro brain show %s once it is registered.\n",
			slug, creation.Repo, strings.ToLower(creation.State), creation.ID, slug)
		return nil
	}
	return waitForBrainCreation(cmd, ctx, client, creation)
}

func brainCreationDone(state string, deploy bool) bool {
	switch strings.ToUpper(state) {
	case "READY", "FAILED":
		return true
	case "REGISTERED":
		return !deploy
	}
	return false
}

func waitForBrainCreation(cmd *cobra.Command, ctx context.Context, client *api.Client, creation *brainCreation) error {
	asJSON := boolFlag(cmd, "json")
	progress := cmd.OutOrStdout()
	if asJSON {
		progress = cmd.ErrOrStderr()
	}
	pollCtx, cancel := context.WithTimeout(ctx, brainWaitTimeout)
	defer cancel()
	last := ""
	for {
		if creation.State != last {
			fmt.Fprintf(progress, "  -> %s\n", strings.ToLower(creation.State))
			last = creation.State
		}
		if brainCreationDone(creation.State, !brainNoDeploy) {
			break
		}
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("timed out waiting for %s (last state: %s)", creation.Slug, strings.ToLower(last))
		case <-time.After(brainPollInterval):
		}
		var resp struct {
			Creation *brainCreation `json:"astroliftBrainCreation"`
		}
		if err := brainGraphQL(pollCtx, client, brainCreationQuery, map[string]interface{}{"id": creation.ID}, &resp, "following "+creation.Slug); err != nil {
			return err
		}
		if resp.Creation == nil {
			return fmt.Errorf("creation %s is no longer visible", creation.ID)
		}
		creation = resp.Creation
	}
	if asJSON {
		if err := renderJSON(cmd, creation); err != nil {
			return err
		}
	}
	if strings.EqualFold(creation.State, "FAILED") {
		if creation.Error != nil {
			return fmt.Errorf("creating %s failed: %s", creation.Slug, firstDeployError([]mutationError{*creation.Error}))
		}
		return fmt.Errorf("creating %s failed", creation.Slug)
	}
	if !asJSON {
		fmt.Fprintf(cmd.OutOrStdout(), "Semantic layer %s is %s. Connect an agent with astro brain connect %s.\n",
			creation.Slug, strings.ToLower(creation.State), creation.Slug)
	}
	return nil
}

// ---- share / unshare --------------------------------------------------------

func brainShareAccess(access string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(access)) {
	case "viewer":
		return "VIEWER", nil
	case "editor":
		return "DEPLOYER", nil
	case "owner":
		return "", errors.New("owners are not granted through astro brain share; change them on the app (grantTeamAccessToApp)")
	}
	return "", errors.New("--access must be viewer or editor")
}

// brainShareTarget reads --team or --agent; exactly one is required.
func brainShareTarget(cmd *cobra.Command) (team, agent string, err error) {
	team, agent = stringFlag(cmd, "team"), strings.TrimSpace(brainAgent)
	if (team == "") == (agent == "") {
		return "", "", errors.New("name a team (--team) or an agent (--agent)")
	}
	return team, agent, nil
}

// requireBrainAgentShares refuses before any write on a server older than
// platform #2449: one that does not report agentShares cannot share with an
// agent either. It also refuses a semantic layer the caller cannot read.
func requireBrainAgentShares(ctx context.Context, client *api.Client, slug string) error {
	b, err := fetchBrain(ctx, client, slug)
	if err != nil {
		return err
	}
	if b.AgentShares == nil {
		return errors.New("this server cannot share a semantic layer with an agent; it needs an Astrolift control plane with platform #2449")
	}
	return nil
}

func runBrainShare(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	team, agent, err := brainShareTarget(cmd)
	if err != nil {
		return err
	}
	if agent != "" {
		return runBrainShareAgent(cmd, ctx, client, slug, agent)
	}
	access, err := brainShareAccess(brainAccess)
	if err != nil {
		return err
	}
	ok, err := brainConfirm(cmd, fmt.Sprintf("Share %s with team %s as %s?", slug, team, brainAccessLabel(access)))
	if err != nil || !ok {
		return err
	}
	var resp struct {
		Result brainResult[brain] `json:"shareBrain"`
	}
	input := map[string]interface{}{"slug": slug, "teamSlug": team, "access": access}
	if err := brainGraphQL(ctx, client, brainShareMutation, map[string]interface{}{"input": input}, &resp, "sharing "+slug); err != nil {
		return err
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return brainFailure("sharing "+slug, resp.Result.Errors)
	}
	return renderBrain(cmd, resp.Result.Data)
}

func runBrainShareAgent(cmd *cobra.Command, ctx context.Context, client *api.Client, slug, agent string) error {
	if !strings.EqualFold(strings.TrimSpace(brainAccess), "viewer") {
		return errors.New("an agent reads a semantic layer; share it with --access viewer")
	}
	if err := requireBrainAgentShares(ctx, client, slug); err != nil {
		return err
	}
	ok, err := brainConfirm(cmd, fmt.Sprintf("Share %s with agent %s as viewer? Its runs that nobody launched can read it when the agent also lists it in brains or brains_allowed.", slug, agent))
	if err != nil || !ok {
		return err
	}
	var resp struct {
		Result brainResult[brain] `json:"shareBrain"`
	}
	input := map[string]interface{}{"slug": slug, "agentSlug": agent, "access": "VIEWER"}
	if err := brainGraphQL(ctx, client, brainShareAgentMutation, map[string]interface{}{"input": input}, &resp, "sharing "+slug); err != nil {
		return err
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return brainFailure("sharing "+slug, resp.Result.Errors)
	}
	return renderBrain(cmd, resp.Result.Data)
}

func runBrainUnshareAgent(cmd *cobra.Command, ctx context.Context, client *api.Client, slug, agent string) error {
	if err := requireBrainAgentShares(ctx, client, slug); err != nil {
		return err
	}
	var resp struct {
		Result brainResult[brain] `json:"unshareBrain"`
	}
	input := map[string]interface{}{"slug": slug, "agentSlug": agent}
	if err := brainGraphQL(ctx, client, brainUnshareAgentMutation, map[string]interface{}{"input": input}, &resp, "unsharing "+slug); err != nil {
		return err
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return brainFailure("unsharing "+slug, resp.Result.Errors)
	}
	if !boolFlag(cmd, "json") {
		fmt.Fprintf(cmd.OutOrStdout(), "Agent %s no longer reads %s; its runs' next token refresh is refused.\n", agent, slug)
	}
	return renderBrain(cmd, resp.Result.Data)
}

func runBrainUnshare(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	team, agent, err := brainShareTarget(cmd)
	if err != nil {
		return err
	}
	if agent != "" {
		return runBrainUnshareAgent(cmd, ctx, client, slug, agent)
	}
	var resp struct {
		Result brainResult[brain] `json:"unshareBrain"`
	}
	input := map[string]interface{}{"slug": slug, "teamSlug": team}
	if err := brainGraphQL(ctx, client, brainUnshareMutation, map[string]interface{}{"input": input}, &resp, "unsharing "+slug); err != nil {
		return err
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return brainFailure("unsharing "+slug, resp.Result.Errors)
	}
	if !boolFlag(cmd, "json") {
		fmt.Fprintf(cmd.OutOrStdout(), "Team %s can no longer get new read tokens for %s; tokens already issued last up to 15 minutes.\n", team, slug)
	}
	return renderBrain(cmd, resp.Result.Data)
}

// ---- host-app ---------------------------------------------------------------

func runBrainHostApp(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, brainSlug string) error {
	if strings.TrimSpace(brainHostImage) == "" {
		return errors.New("--image is required")
	}
	project, err := resolveProjectForResource(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	input := map[string]interface{}{
		"brainSlug": brainSlug,
		"projectId": project.ID,
		"image":     strings.TrimSpace(brainHostImage),
	}
	if v := strings.TrimSpace(brainHostName); v != "" {
		input["name"] = v
	}
	if v := strings.TrimSpace(brainHostSlug); v != "" {
		input["slug"] = v
	}
	if cmd.Flags().Changed("port") {
		input["port"] = brainHostPort
	}
	if err := applyCreationExpiry(cmd, ctx, client, input, ""); err != nil {
		return err
	}
	var resp struct {
		Result brainResult[brainHostedAppResult] `json:"hostAppOnBrain"`
	}
	mutation := brainHostAppMutation
	if serverAdvertises(ctx, client, "apps.temporary_expiry") {
		mutation = strings.Replace(mutation, "app { id slug name }", "app { id slug name expiresAt }", 1)
	}
	if err := brainGraphQL(ctx, client, mutation, map[string]interface{}{"input": input}, &resp, "hosting an app on "+brainSlug); err != nil {
		return err
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return brainFailure("hosting an app on "+brainSlug, resp.Result.Errors)
	}
	hosted := resp.Result.Data
	if !brainHostDeploy {
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, hosted)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Created %s on %s [%s]; each deploy gets %s.\nDeploy it with astro app deploy --app %s.\n",
			hosted.App.Slug, hosted.BrainSlug, hosted.EnvironmentName, strings.Join(hosted.EnvKeys, ", "), hosted.App.Slug)
		return nil
	}
	if !boolFlag(cmd, "json") {
		fmt.Fprintf(cmd.OutOrStdout(), "Created %s on %s [%s].\n", hosted.App.Slug, hosted.BrainSlug, hosted.EnvironmentName)
	}
	appDeployEnv = hosted.EnvironmentName
	appDeployImageTag = ""
	appDeployRef = ""
	appDeployWait = brainWait
	return runAppDeploy(cmd, ctx, client, hosted.App.Slug)
}

// ---- rebuild ----------------------------------------------------------------

func runBrainRebuild(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	var resp struct {
		Result brainResult[brainRebuild] `json:"rebuildBrain"`
	}
	input := map[string]interface{}{"slug": slug, "environmentName": brainEnv, "ingest": brainIngest}
	if err := brainGraphQL(ctx, client, brainRebuildMutation, map[string]interface{}{"input": input}, &resp, "rebuilding "+slug); err != nil {
		return err
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return brainFailure("rebuilding "+slug, resp.Result.Errors)
	}
	r := resp.Result.Data
	if boolFlag(cmd, "json") && (!brainWait || r.DeploymentID == nil) {
		return renderJSON(cmd, r)
	}
	out := cmd.OutOrStdout()
	switch r.Mode {
	case "ingest":
		fmt.Fprintf(out, "Ingest dispatched for %s: %s\nIts commit deploys through the normal push path; check it with astro brain status %s.\n", slug, derefOr(r.IngestRunURL, "-"), slug)
	case "ci":
		fmt.Fprintf(out, "CI dispatched for %s: %s\n", slug, derefOr(r.RunURL, "-"))
	default:
		fmt.Fprintf(out, "Deployment started for %s: %s\n", slug, derefOr(r.DeploymentID, "-"))
	}
	if len(r.Rebuilds) > 0 {
		fmt.Fprintf(out, "Rebuilds: %s\n", strings.Join(r.Rebuilds, ", "))
	}
	if brainWait {
		if r.DeploymentID == nil {
			fmt.Fprintln(out, "Nothing to wait for here: the run deploys when it pushes.")
			return nil
		}
		return waitForDeployment(cmd, ctx, client, *r.DeploymentID)
	}
	return nil
}

func derefOr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

// ---- substrate --------------------------------------------------------------

func runBrainSubstrate(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	b, err := fetchBrain(ctx, client, slug)
	if err != nil {
		return err
	}
	if !brainSubstrateFlagsSet(cmd) {
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, b.Substrate)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		renderBrainSubstrate(w, b.Substrate)
		return w.Flush()
	}
	substrate, err := brainSubstrateInput(cmd, b.Substrate)
	if err != nil {
		return err
	}
	ok, err := brainConfirm(cmd, fmt.Sprintf("Commit the new substrate to %s's deploy branch and deploy it?", slug))
	if err != nil || !ok {
		return err
	}
	var resp struct {
		Result brainResult[brain] `json:"updateBrainSubstrate"`
	}
	input := map[string]interface{}{"slug": slug, "substrate": substrate}
	if err := brainGraphQL(ctx, client, brainSubstrateMutation, map[string]interface{}{"input": input}, &resp, "changing the substrate of "+slug); err != nil {
		return err
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return brainFailure("changing the substrate of "+slug, resp.Result.Errors)
	}
	return renderBrain(cmd, resp.Result.Data)
}

// ---- delete -----------------------------------------------------------------

func runBrainDelete(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	ok, err := brainConfirm(cmd, fmt.Sprintf("Archive %s? It scales to zero, leaves the register, and every hosted app loses its token. The repository is kept.", slug))
	if err != nil || !ok {
		return err
	}
	var resp struct {
		Result brainResult[brain] `json:"archiveBrain"`
	}
	if err := brainGraphQL(ctx, client, brainDeleteMutation, map[string]interface{}{"input": map[string]interface{}{"slug": slug}}, &resp, "deleting "+slug); err != nil {
		return err
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return brainFailure("deleting "+slug, resp.Result.Errors)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Archived %s. The repository, databases and volumes are kept; restoreApp brings it back, and hosted apps are hosted again.\n", slug)
	return nil
}

// ---- commands ---------------------------------------------------------------

var brainCmd = &cobra.Command{
	Use:   "brain",
	Short: "Manage semantic layers (Mimwell brains)",
	Long: `Set up, host and manage semantic layers: Mimwell brains registered as apps
of kind "mimwell". Each command names the semantic layer by its app slug, and
one you cannot read reads the same as one that does not exist.

Connect an agent with 'astro brain connect', create one from the Mimwell
edition with 'astro brain create', and follow its freshness with
'astro brain status'. Needs a control plane with platform #2424; older ones
say so in one line.`,
}

var brainListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the semantic layers you can read",
	Args:  cobra.NoArgs,
	RunE: brainRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, _ []string) error {
		return runBrainList(cmd, ctx, client)
	}),
}

var brainShowCmd = &cobra.Command{
	Use:   "show <slug>",
	Short: "Show a semantic layer: team, project, environments, shares, hosted apps, substrate",
	Args:  cobra.ExactArgs(1),
	RunE: brainRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, args []string) error {
		return runBrainShow(cmd, ctx, client, args[0])
	}),
}

var brainStatusCmd = &cobra.Command{
	Use:   "status <slug>",
	Short: "Show deploy state, running vs head commit, compile time and last ingest",
	Args:  cobra.ExactArgs(1),
	RunE: brainRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, args []string) error {
		return runBrainStatus(cmd, ctx, client, args[0])
	}),
}

var brainConnectCmd = &cobra.Command{
	Use:   "connect <slug>",
	Short: "Print an MCP client config for a semantic layer with a fresh 15-minute token",
	Long: `Issue a 15-minute read token for a semantic layer and print a client
config for its MCP endpoint. Run it again to refresh the token.

  --format mcp     {"mcpServers": {...}} for any MCP client (default)
  --format claude  a 'claude mcp add-json' command
  --format env     BRAIN_MCP_URL, BRAIN_TOKEN_HEADER and BRAIN_TOKEN

--write puts the output in a file of its own with mode 0600 (for example a
file passed to 'claude --mcp-config'), replacing what an earlier run wrote.`,
	Args: cobra.ExactArgs(1),
	RunE: brainRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, args []string) error {
		return runBrainConnect(cmd, ctx, client, args[0])
	}),
}

var brainCreateCmd = &cobra.Command{
	Use:   "create <slug> --project <p> --connection <c> --repo <owner/name>",
	Short: "Create a semantic layer from the Mimwell edition, register it and deploy it",
	Long: `Scaffold an empty or new repository from the verified Mimwell edition in
one commit, register it as a semantic layer and deploy it. The slug is the
app's slug, with or without -brain (acme, acme-brain); one without it is
written to the brain's config as deploy.app_slug, which the edition must
have. The server works in the background; --wait follows it to the end.

A GitHub App connection cannot create repositories: create the empty
repository first, or use a user OAuth or PAT connection. The substrate flags
choose the Mimwell cell; without them it is the files cell with no
persistence.`,
	Args: cobra.ExactArgs(1),
	RunE: brainRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, args []string) error {
		return runBrainCreate(cmd, ctx, client, cfg, args[0])
	}),
}

var brainShareCmd = &cobra.Command{
	Use:   "share <slug> --team <t> --access viewer|editor | --agent <a>",
	Short: "Share a semantic layer with a team (viewer or editor) or an agent (viewer)",
	Long: `Share a semantic layer with a team. viewer reads it; editor also edits and
deploys it (the app's deployer access, which you must hold yourself). Owners
are not granted here. Asks first unless --yes; an approval rule on
brain.share still applies.

With --agent instead of --team, the agent's runs that nobody launched
(schedule, loop, webhook, workflow stage) read the layer as the agent, viewer
only, when the agent also lists it in brains or brains_allowed. Runs a person
launches still read as that person. Needs platform #2449.`,
	Args: cobra.ExactArgs(1),
	RunE: brainRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, args []string) error {
		return runBrainShare(cmd, ctx, client, args[0])
	}),
}

var brainUnshareCmd = &cobra.Command{
	Use:   "unshare <slug> --team <t> | --agent <a>",
	Short: "Stop sharing a semantic layer with a team or an agent",
	Args:  cobra.ExactArgs(1),
	RunE: brainRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, args []string) error {
		return runBrainUnshare(cmd, ctx, client, args[0])
	}),
}

var brainHostAppCmd = &cobra.Command{
	Use:   "host-app <brain> --project <p> --image <ref>",
	Short: "Create an app bound to a semantic layer, with its MCP URL and a read token on each deploy",
	Args:  cobra.ExactArgs(1),
	RunE: brainRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, args []string) error {
		return runBrainHostApp(cmd, ctx, client, cfg, args[0])
	}),
}

var brainRebuildCmd = &cobra.Command{
	Use:   "rebuild <slug>",
	Short: "Redeploy a semantic layer's branch head, or run its ingest first with --ingest",
	Long: `Redeploy the deploy branch head (a platform build starts a deployment, a
CI-built semantic layer dispatches its CI). With --ingest, run the
knowledge-refresh workflow instead; its commit deploys through the normal
push path.`,
	Args: cobra.ExactArgs(1),
	RunE: brainRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, args []string) error {
		return runBrainRebuild(cmd, ctx, client, args[0])
	}),
}

var brainSubstrateCmd = &cobra.Command{
	Use:   "substrate <slug>",
	Short: "Show a semantic layer's substrate, or graduate it to another cell",
	Long: `Without flags, show the cell and resources. With substrate flags, check the
new cell against the Mimwell matrix, commit it to the deploy branch and
deploy it; a flag left out keeps its current value. Persistence that holds
data (postgres, sqlite-volume, efs) is never dropped.`,
	Args: cobra.ExactArgs(1),
	RunE: brainRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, args []string) error {
		return runBrainSubstrate(cmd, ctx, client, args[0])
	}),
}

var brainDeleteCmd = &cobra.Command{
	Use:   "delete <slug>",
	Short: "Archive a semantic layer and revoke its hosted apps' tokens; the repository is kept",
	Long: `Archive a semantic layer: scale it to zero, drop it from the register and
revoke every hosted app's binding and token. The repository, databases and
volumes are kept, and restoreApp brings it back. Asks first unless --yes; an
approval rule on brain.delete still applies.`,
	Args: cobra.ExactArgs(1),
	RunE: brainRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, args []string) error {
		return runBrainDelete(cmd, ctx, client, args[0])
	}),
}

func addBrainSubstrateFlags(c *cobra.Command) {
	c.Flags().StringVar(&brainAuthority, "authority", "files", "Where canon lives: files, sqlite, postgres, mysql, mongo, d1")
	c.Flags().StringVar(&brainBlobs, "blobs", "fs", "Where blobs live: fs, db-blob, s3, azure-blob")
	c.Flags().StringVar(&brainVectors, "vectors", "local", "Where vectors live: local, in-db, edge-db, external")
	c.Flags().StringVar(&brainGraph, "graph", "edges-table", "Where the graph lives: edges-table, age")
	c.Flags().StringSliceVar(&brainPersistence, "persistence", nil, "Persistence (repeatable): none, sqlite-volume, efs, postgres, s3, d1, kv")
	c.Flags().IntVar(&brainReplicas, "replicas", 1, "Replicas")
	c.Flags().StringVar(&brainImage, "image", "", "Prebuilt image instead of the edition's portal Dockerfile")
	c.Flags().IntVar(&brainPort, "port", 8080, "Container port")
}

func init() {
	brainListCmd.Flags().StringVar(&brainSearch, "search", "", "Only semantic layers whose slug or name matches")

	brainStatusCmd.Flags().StringVar(&brainEnv, "env", "production", "Environment")

	brainConnectCmd.Flags().StringVar(&brainConnectEnv, "env", "", "Environment (default production, or the only served one)")
	brainConnectCmd.Flags().StringVar(&brainFormat, "format", "mcp", "Output: mcp, claude or env")
	brainConnectCmd.Flags().StringVar(&brainWrite, "write", "", "Write the output to this file (mode 0600) instead of stdout")

	brainCreateCmd.Flags().StringVar(&brainConnectionSel, "connection", "", "Source connection id, name or account (see astro scm list)")
	brainCreateCmd.Flags().StringVar(&brainRepo, "repo", "", "Repository owner/name; empty or not yet created")
	brainCreateCmd.Flags().StringVar(&brainName, "name", "", "Display name (default: the slug)")
	brainCreateCmd.Flags().StringVar(&brainVisibility, "visibility", "private", "Visibility of a new repository: private, internal or public")
	brainCreateCmd.Flags().StringVar(&brainTemplateRef, "template-ref", "", "Mimwell edition tag or commit (default: the install's pinned edition)")
	brainCreateCmd.Flags().StringVar(&brainEnv, "env", "production", "Environment to deploy")
	brainCreateCmd.Flags().BoolVar(&brainNoDeploy, "no-deploy", false, "Register without deploying")
	brainCreateCmd.Flags().BoolVar(&brainWait, "wait", false, "Follow the creation until it is ready or fails")
	addBrainSubstrateFlags(brainCreateCmd)

	brainShareCmd.Flags().StringVar(&brainAccess, "access", "viewer", "viewer (reads) or editor (also edits and deploys)")
	brainShareCmd.Flags().BoolVar(&brainYes, "yes", false, "Don't ask for confirmation")
	brainShareCmd.Flags().StringVar(&brainAgent, "agent", "", "Share with this agent instead of a team (viewer only)")
	brainUnshareCmd.Flags().StringVar(&brainAgent, "agent", "", "Stop sharing with this agent instead of a team")

	brainHostAppCmd.Flags().StringVar(&brainHostImage, "image", "", "Image the app runs")
	brainHostAppCmd.Flags().StringVar(&brainHostName, "name", "", "Display name")
	brainHostAppCmd.Flags().StringVar(&brainHostSlug, "slug", "", "App slug")
	brainHostAppCmd.Flags().IntVar(&brainHostPort, "port", 0, "Container port")
	brainHostAppCmd.Flags().BoolVar(&brainHostDeploy, "deploy", false, "Deploy it right away")
	brainHostAppCmd.Flags().BoolVar(&brainWait, "wait", false, "With --deploy, wait for the deployment")

	brainRebuildCmd.Flags().StringVar(&brainEnv, "env", "production", "Environment")
	brainRebuildCmd.Flags().BoolVar(&brainIngest, "ingest", false, "Run the knowledge-refresh workflow first; its push deploys")
	brainRebuildCmd.Flags().BoolVar(&brainWait, "wait", false, "Wait for the deployment (a platform build)")

	addBrainSubstrateFlags(brainSubstrateCmd)
	brainSubstrateCmd.Flags().BoolVar(&brainYes, "yes", false, "Don't ask for confirmation")

	brainDeleteCmd.Flags().BoolVar(&brainYes, "yes", false, "Don't ask for confirmation")

	brainCmd.AddCommand(brainListCmd, brainShowCmd, brainStatusCmd, brainConnectCmd, brainCreateCmd,
		brainShareCmd, brainUnshareCmd, brainHostAppCmd, brainRebuildCmd, brainSubstrateCmd, brainDeleteCmd)
	rootCmd.AddCommand(brainCmd)
}
