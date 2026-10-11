// Zentinelle connection and per-cluster gateway commands (#113, platform #1888).
//
// Wrappers over the control plane's existing Zentinelle GraphQL: the status
// query and the connect, disconnect, register, unregister, gateway on/off and
// credential rotation mutations. The server gates each on its Zentinelle
// permission; refusals are passed through with the server's reason. Usage,
// policy and audit reads arrive when the control plane exposes them.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

type zentinelleGateway struct {
	ClusterID           string  `json:"clusterId"`
	ClusterSlug         string  `json:"clusterSlug"`
	ZentinelleClusterID string  `json:"zentinelleClusterId"`
	GatewayName         string  `json:"gatewayName"`
	GatewayEnabled      bool    `json:"gatewayEnabled"`
	GatewayDeployed     bool    `json:"gatewayDeployed"`
	Status              string  `json:"status"`
	RegisteredAt        *string `json:"registeredAt"`
	CredentialRotatedAt *string `json:"credentialRotatedAt"`
	LastError           string  `json:"lastError"`
	Unregistered        bool    `json:"unregistered"`
}

type zentinelleConnection struct {
	ID                    string              `json:"id"`
	BaseURL               string              `json:"baseUrl"`
	Status                string              `json:"status"`
	ZentinelleInstallID   string              `json:"zentinelleInstallId"`
	ConnectedAt           *string             `json:"connectedAt"`
	LastError             string              `json:"lastError"`
	GatewayFeatureEnabled bool                `json:"gatewayFeatureEnabled"`
	Clusters              []zentinelleGateway `json:"clusters"`
}

const zentinelleGatewayFields = `clusterId clusterSlug zentinelleClusterId gatewayName gatewayEnabled gatewayDeployed status registeredAt credentialRotatedAt lastError unregistered`
const zentinelleConnectionFields = `id baseUrl status zentinelleInstallId connectedAt lastError gatewayFeatureEnabled clusters { ` + zentinelleGatewayFields + ` }`

var (
	zentinelleURL        string
	zentinelleCode       string
	zentinelleCodeStdin  bool
	zentinelleForce      bool
	zentinelleYes        bool
	zentinelleOverlapSec int
)

var zentinelleCmd = &cobra.Command{
	Use:   "zentinelle",
	Short: "Zentinelle connection and per-cluster gateways",
	Long: `Connect the working organization to Zentinelle and manage the model
gateway Zentinelle runs on each registered cluster. Each command needs the
matching Zentinelle permission; the server's refusal is shown as is.`,
}

var zentinelleStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the Zentinelle connection and each cluster's gateway",
	RunE:  zentinelleRun(runZentinelleStatus),
}

var zentinelleConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Connect the working organization to Zentinelle with an enrollment code",
	RunE:  zentinelleRun(runZentinelleConnect),
}

var zentinelleDisconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "Revoke the install in Zentinelle and remove every gateway it registered",
	RunE:  zentinelleRun(runZentinelleDisconnect),
}

var zentinelleClusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Register clusters with Zentinelle and control their gateways",
}

// zentinelleClusterVerb builds a per-cluster command. confirm, when set, is
// the refusal shown unless --yes was passed, checked before any request.
func zentinelleClusterVerb(use, short, confirm string, run func(*cobra.Command, context.Context, *api.Client, string) error) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <cluster>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if confirm != "" && !zentinelleYes {
				return errors.New(confirm)
			}
			client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
			if err != nil {
				return err
			}
			if _, err := resolveOrg(cmd, cmd.Context(), client, cfg); err != nil {
				return err
			}
			cluster, err := fetchClusterBySlug(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}
			return run(cmd, cmd.Context(), client, cluster.ID)
		},
	}
}

var (
	zentinelleRegisterCmd = zentinelleClusterVerb("register", "Register a cluster and deploy its gateway", "",
		func(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
			return runGatewayMutation(cmd, ctx, client, "registerZentinelleCluster", "ZentinelleClusterInput",
				map[string]interface{}{"clusterId": id}, "Registered")
		})
	zentinelleUnregisterCmd = zentinelleClusterVerb("unregister", "Revoke a cluster in Zentinelle and remove its gateway",
		"unregistering revokes the cluster's gateway credential; pass --yes to confirm",
		func(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
			return runGatewayMutation(cmd, ctx, client, "unregisterZentinelleCluster", "ZentinelleClusterInput",
				map[string]interface{}{"clusterId": id}, "Unregistered")
		})
	zentinelleEnableCmd = zentinelleClusterVerb("enable", "Turn a registered cluster's gateway on", "",
		func(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
			return runGatewayMutation(cmd, ctx, client, "setZentinelleGatewayEnabled", "SetZentinelleGatewayEnabledInput",
				map[string]interface{}{"clusterId": id, "enabled": true}, "Enabled the gateway on")
		})
	zentinelleDisableCmd = zentinelleClusterVerb("disable", "Turn a registered cluster's gateway off", "",
		func(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
			return runGatewayMutation(cmd, ctx, client, "setZentinelleGatewayEnabled", "SetZentinelleGatewayEnabledInput",
				map[string]interface{}{"clusterId": id, "enabled": false}, "Disabled the gateway on")
		})
	zentinelleRotateCmd = zentinelleClusterVerb("rotate", "Rotate a cluster's gateway credential", "",
		func(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
			input := map[string]interface{}{"clusterId": id}
			if cmd.Flags().Changed("overlap-seconds") {
				input["overlapSeconds"] = zentinelleOverlapSec
			}
			return runGatewayMutation(cmd, ctx, client, "rotateZentinelleGatewayCredential",
				"RotateZentinelleGatewayCredentialInput", input, "Rotated the gateway credential on")
		})
)

// zentinelleRun loads the client and working org before an org-level command.
func zentinelleRun(run func(*cobra.Command, context.Context, *api.Client, *config.Config, string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		client, cfg, entry, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		server := ""
		if entry != nil {
			server = entry.APIURL
		}
		return run(cmd, cmd.Context(), client, cfg, server)
	}
}

func runZentinelleStatus(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, server string) error {
	if _, err := resolveOrg(cmd, ctx, client, cfg); err != nil {
		return err
	}
	var resp struct {
		Connection *zentinelleConnection `json:"astroliftZentinelleConnection"`
	}
	if err := client.GraphQL(ctx, `query { astroliftZentinelleConnection { `+zentinelleConnectionFields+` } }`, nil, &resp); err != nil {
		return fmt.Errorf("reading the Zentinelle connection: %w", err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, struct {
			Connected  bool                  `json:"connected"`
			Connection *zentinelleConnection `json:"connection"`
		}{resp.Connection != nil, resp.Connection})
	}
	out := cmd.OutOrStdout()
	if resp.Connection == nil {
		fmt.Fprintf(out, "Zentinelle is not connected on %s.\n", server)
		return nil
	}
	c := resp.Connection
	fmt.Fprintf(out, "Zentinelle: %s (%s)\n", c.BaseURL, c.Status)
	if c.LastError != "" {
		fmt.Fprintf(out, "Error:      %s\n", c.LastError)
	}
	if !c.GatewayFeatureEnabled {
		fmt.Fprintln(out, "Gateways:   off on this install (ZENTINELLE_GATEWAY_ENABLED)")
	}
	live := 0
	for _, g := range c.Clusters {
		if !g.Unregistered {
			live++
		}
	}
	if live == 0 {
		fmt.Fprintln(out, "Clusters:   none registered")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CLUSTER\tSTATUS\tENABLED\tDEPLOYED\tERROR")
	for _, g := range c.Clusters {
		if g.Unregistered {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%t\t%t\t%s\n", g.ClusterSlug, g.Status, g.GatewayEnabled, g.GatewayDeployed, g.LastError)
	}
	return tw.Flush()
}

func runZentinelleConnect(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, _ string) error {
	code := strings.TrimSpace(zentinelleCode)
	if zentinelleCodeStdin {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return fmt.Errorf("reading the enrollment code from stdin: %w", err)
		}
		code = strings.TrimSpace(string(data))
	}
	if strings.TrimSpace(zentinelleURL) == "" || code == "" {
		return errors.New("connect needs --url and an enrollment code (--code or --code-stdin)")
	}
	if _, err := resolveOrg(cmd, ctx, client, cfg); err != nil {
		return err
	}
	var resp struct {
		Result struct {
			Ok     bool                  `json:"ok"`
			Errors []mutationError       `json:"errors"`
			Data   *zentinelleConnection `json:"data"`
		} `json:"connectZentinelle"`
	}
	mutation := `mutation($input: ConnectZentinelleInput!) {
  connectZentinelle(input: $input) { ok errors { code message field } data { ` + zentinelleConnectionFields + ` } }
}`
	input := map[string]interface{}{"url": strings.TrimSpace(zentinelleURL), "enrollmentCode": code}
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("connecting to Zentinelle: %w", err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("connect refused: %s", firstDeployError(resp.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Connected to Zentinelle at %s (%s)\n", resp.Result.Data.BaseURL, resp.Result.Data.Status)
	return nil
}

func runZentinelleDisconnect(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, _ string) error {
	if !zentinelleYes {
		return errors.New("disconnecting revokes the install in Zentinelle and removes every gateway; pass --yes to confirm")
	}
	if _, err := resolveOrg(cmd, ctx, client, cfg); err != nil {
		return err
	}
	var resp struct {
		Result struct {
			Ok     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *struct {
				Connection zentinelleConnection `json:"connection"`
				Warnings   []string             `json:"warnings"`
			} `json:"data"`
		} `json:"disconnectZentinelle"`
	}
	mutation := `mutation($input: DisconnectZentinelleInput!) {
  disconnectZentinelle(input: $input) {
    ok errors { code message field }
    data { connection { ` + zentinelleConnectionFields + ` } warnings }
  }
}`
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": map[string]interface{}{"force": zentinelleForce}}, &resp); err != nil {
		return fmt.Errorf("disconnecting from Zentinelle: %w", err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("disconnect refused: %s", firstDeployError(resp.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Disconnected from Zentinelle.")
	for _, w := range resp.Result.Data.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", w)
	}
	return nil
}

func runGatewayMutation(cmd *cobra.Command, ctx context.Context, client *api.Client, field, inputType string, input map[string]interface{}, verb string) error {
	var resp map[string]struct {
		Ok     bool               `json:"ok"`
		Errors []mutationError    `json:"errors"`
		Data   *zentinelleGateway `json:"data"`
	}
	mutation := fmt.Sprintf(`mutation($input: %s!) {
  %s(input: $input) { ok errors { code message field } data { %s } }
}`, inputType, field, zentinelleGatewayFields)
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return err
	}
	result := resp[field]
	if !result.Ok || result.Data == nil {
		return fmt.Errorf("%s refused: %s", field, firstDeployError(result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s)\n", verb, result.Data.ClusterSlug, result.Data.Status)
	return nil
}

func init() {
	zentinelleConnectCmd.Flags().StringVar(&zentinelleURL, "url", "", "Zentinelle base URL")
	zentinelleConnectCmd.Flags().StringVar(&zentinelleCode, "code", "", "Enrollment code from Zentinelle (prefer --code-stdin)")
	zentinelleConnectCmd.Flags().BoolVar(&zentinelleCodeStdin, "code-stdin", false, "Read the enrollment code from stdin")
	zentinelleDisconnectCmd.Flags().BoolVar(&zentinelleForce, "force", false, "Disconnect even if Zentinelle cannot be reached")
	zentinelleDisconnectCmd.Flags().BoolVar(&zentinelleYes, "yes", false, "Confirm the disconnect")
	zentinelleUnregisterCmd.Flags().BoolVar(&zentinelleYes, "yes", false, "Confirm the unregister")
	zentinelleRotateCmd.Flags().IntVar(&zentinelleOverlapSec, "overlap-seconds", 0, "Keep the old credential valid this long (server default when unset)")

	zentinelleClusterCmd.AddCommand(zentinelleRegisterCmd, zentinelleUnregisterCmd, zentinelleEnableCmd, zentinelleDisableCmd, zentinelleRotateCmd)
	zentinelleCmd.AddCommand(zentinelleStatusCmd, zentinelleConnectCmd, zentinelleDisconnectCmd, zentinelleClusterCmd)
	rootCmd.AddCommand(zentinelleCmd)
}
