package cmd

import (
	"context"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// authStatusReport is `astro auth status --json`. Capabilities carries only
// what was actually established: an IDE reads an absent key as "unknown",
// never as denied (#86, calliope-vscode#667).
type authStatusReport struct {
	Server       string                 `json:"server,omitempty"`
	APIURL       string                 `json:"apiUrl,omitempty"`
	LoggedIn     bool                   `json:"loggedIn"`
	ExpiresAt    *time.Time             `json:"expiresAt,omitempty"`
	Expired      bool                   `json:"expired"`
	Capabilities map[string]interface{} `json:"capabilities,omitempty"`
}

// boxCapabilities reports whether the viewer may start a box on a custom
// image, when the server enforces the permission and the working org is
// known without a prompt. Any failure leaves it unreported.
func boxCapabilities(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) map[string]interface{} {
	var info struct {
		Info *struct {
			Capabilities []string `json:"capabilities"`
		} `json:"astroliftServerInfo"`
	}
	if client.GraphQL(ctx, `query { astroliftServerInfo { capabilities } }`, nil, &info) != nil || info.Info == nil {
		return nil
	}
	enforced := false
	for _, capability := range info.Info.Capabilities {
		enforced = enforced || capability == "boxes.custom_image_permission"
	}
	if !enforced {
		return nil
	}
	org, ok := orgWithoutPrompt(cmd, ctx, client, cfg)
	if !ok {
		return nil
	}
	client.SetOrg(org.ID)
	var perms struct {
		Permissions []string `json:"astroliftMyPermissions"`
	}
	if client.GraphQL(ctx, `query { astroliftMyPermissions }`, nil, &perms) != nil {
		return nil
	}
	allowed := false
	for _, slug := range perms.Permissions {
		allowed = allowed || slug == "agent_box.custom_image"
	}
	return map[string]interface{}{"box": map[string]interface{}{"customImage": allowed}}
}

// orgWithoutPrompt picks the working org from --org, the saved default or a
// single membership; anything ambiguous returns false rather than prompting.
func orgWithoutPrompt(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) (orgRef, bool) {
	orgs, err := listOrgs(ctx, client)
	if err != nil || len(orgs) == 0 {
		return orgRef{}, false
	}
	want := orgFlagValue(cmd)
	if want == "" {
		want = cfg.DefaultOrg
	}
	if want == "" {
		return orgs[0], len(orgs) == 1
	}
	for _, o := range orgs {
		if o.Slug == want || o.ID == want {
			return o, true
		}
	}
	return orgRef{}, false
}
