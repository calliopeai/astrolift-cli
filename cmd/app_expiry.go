package cmd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const expiryUnavailable = "selected server does not support temporary apps; upgrade the server"

var expiryDuration = regexp.MustCompile(`^[1-9][0-9]*(m|h|d|w|mo)$`)

func creationExpiryFlags(cmd *cobra.Command) {
	cmd.Flags().String("ttl", "", "Tear down after this duration (30m, 4h, 7d, 6w, 3mo)")
	cmd.Flags().String("expires-at", "", "Tear down at this RFC 3339 timestamp")
	cmd.MarkFlagsMutuallyExclusive("ttl", "expires-at")
}

func expiryInput(ttl, until string) (map[string]interface{}, error) {
	input := map[string]interface{}{}
	if ttl != "" && until != "" {
		return nil, errors.New("choose a duration or an absolute expiry, not both")
	}
	if ttl != "" {
		if !expiryDuration.MatchString(ttl) {
			return nil, errors.New("duration must be a positive integer followed by m, h, d, w or mo")
		}
		input["expiresIn"] = ttl
	}
	if until != "" {
		at, err := time.Parse(time.RFC3339, until)
		if err != nil {
			return nil, errors.New("expiry must be an RFC 3339 timestamp with a timezone")
		}
		if !at.After(time.Now()) {
			return nil, errors.New("expiry must be in the future")
		}
		input["expiresAt"] = at.Format(time.RFC3339)
	}
	return input, nil
}

func requireServerFeature(ctx context.Context, client *api.Client, capability, label string) error {
	var response struct {
		Info *struct {
			Capabilities []string `json:"capabilities"`
		} `json:"astroliftServerInfo"`
	}
	err := client.PublicGraphQL(ctx, `query ClientCapabilities { astroliftServerInfo { capabilities } }`, nil, &response)
	if errors.Is(err, api.ErrSchemaMismatch) {
		return fmt.Errorf("selected server does not support %s; upgrade the server", label)
	}
	if err != nil {
		return fmt.Errorf("discovering server capabilities: %w", err)
	}
	if response.Info != nil {
		for _, value := range response.Info.Capabilities {
			if value == capability {
				return nil
			}
		}
	}
	return fmt.Errorf("selected server does not support %s; upgrade the server", label)
}

func requireExpiry(ctx context.Context, client *api.Client) error {
	return requireServerFeature(ctx, client, "apps.temporary_expiry", "temporary apps")
}

func applyCreationExpiry(cmd *cobra.Command, ctx context.Context, client *api.Client, input map[string]interface{}, manifestTTL string) error {
	ttl, until := stringFlag(cmd, "ttl"), stringFlag(cmd, "expires-at")
	if ttl == "" && until == "" {
		ttl = manifestTTL
	}
	fields, err := expiryInput(ttl, until)
	if err != nil {
		return err
	}
	if len(fields) == 0 {
		return nil
	}
	if err := requireExpiry(ctx, client); err != nil {
		return err
	}
	for k, v := range fields {
		input[k] = v
	}
	return nil
}

// Schema validation rejects an unknown selection before executing it. Only that
// narrow error permits retrying the older read; authorization failures surface.
func queryWithExpiry(cmd *cobra.Command, ctx context.Context, client *api.Client, query string, vars map[string]interface{}, target interface{}, required bool) (bool, error) {
	enhanced := strings.Replace(query, "    slug\n", "    slug\n    expiresAt\n", 1)
	err := client.GraphQL(ctx, enhanced, vars, target)
	if !errors.Is(err, api.ErrSchemaMismatch) {
		return err == nil, err
	}
	if required {
		return false, errors.New(expiryUnavailable)
	}
	fmt.Fprintln(cmd.ErrOrStderr(), expiryUnavailable)
	return false, client.GraphQL(ctx, query, vars, target)
}

func appExpiryLabel(value *string, available bool) string {
	if !available {
		return "unavailable"
	}
	return expiryLabel(value)
}

func expiryLabel(value *string) string {
	if value == nil {
		return "permanent"
	}
	return *value
}

func runAppExpiry(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string, clear bool) error {
	input := map[string]interface{}{"appSlug": slug}
	if clear {
		if !boolFlag(cmd, "clear") {
			return errors.New("pass --clear to make the app permanent")
		}
		if !boolFlag(cmd, "yes") {
			if boolFlag(cmd, "no-prompt") {
				return errors.New("making an app permanent requires --yes when --no-prompt is set")
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Make %s permanent? [y/N] ", slug)
			var answer string
			_, _ = fmt.Fscan(cmd.InOrStdin(), &answer)
			if !strings.EqualFold(strings.TrimSpace(answer), "y") {
				return errors.New("cancelled; expiry was not changed")
			}
		}
		input["clear"] = true
	} else {
		by, until := stringFlag(cmd, "by"), stringFlag(cmd, "until")
		if by == "" && until == "" {
			return errors.New("pass exactly one of --by or --until")
		}
		fields, err := expiryInput(by, until)
		if err != nil {
			return err
		}
		for k, v := range fields {
			if k == "expiresIn" {
				k = "extendBy"
			}
			input[k] = v
		}
	}
	if err := requireExpiry(ctx, client); err != nil {
		return err
	}
	var response struct {
		Result struct {
			OK     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *struct {
				ID         string  `json:"id"`
				Slug       string  `json:"slug"`
				ExpiresAt  *string `json:"expiresAt"`
				DeleteData bool    `json:"deleteDataOnExpiry"`
			} `json:"data"`
		} `json:"setAppExpiry"`
	}
	query := `mutation ChangeExpiry($input:SetAppExpiryInput!) { setAppExpiry(input:$input) { ok errors { code message field } data { id slug expiresAt deleteDataOnExpiry } } }`
	if err := client.GraphQL(ctx, query, map[string]interface{}{"input": input}, &response); err != nil {
		return err
	}
	if !response.Result.OK || response.Result.Data == nil {
		return fmt.Errorf("changing expiry: %s", firstDeployError(response.Result.Errors))
	}
	if response.Result.Data.Slug != slug {
		return errors.New("server returned a different app; reread the original app before retrying")
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, response.Result.Data)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s expires: %s\n", slug, expiryLabel(response.Result.Data.ExpiresAt))
	return err
}

func expiryCommand(use, short string, clear bool) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppExpiry(cmd, cmd.Context(), client, args[0], clear)
	}}
}

func init() {
	creationExpiryFlags(appRegisterCmd)
	creationExpiryFlags(brainHostAppCmd)
	appListCmd.Flags().Bool("temporary", false, "Show only apps with an expiry")
	extend := expiryCommand("extend <app-slug>", "Move an app's expiry later", false)
	extend.Flags().String("by", "", "Extend the current expiry by a duration")
	extend.Flags().String("until", "", "Extend until an RFC 3339 timestamp")
	extend.MarkFlagsMutuallyExclusive("by", "until")
	clear := expiryCommand("expiry <app-slug>", "Make a temporary app permanent", true)
	clear.Flags().Bool("clear", false, "Clear the expiry (subject to the organization's permissions and policy)")
	clear.Flags().BoolP("yes", "y", false, "Confirm making the app permanent")
	appCmd.AddCommand(extend, clear)
}
