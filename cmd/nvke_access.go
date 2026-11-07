package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

func newNVKEAccessCommand() *cobra.Command {
	group := &cobra.Command{Use: "access", Short: "Enroll and revoke imported workload credential ownership grants", Long: `Manage explicit imported-cluster access bindings in Astro. These commands do not
download kubeconfigs or modify native infrastructure. Enrollment requires admin
and read permissions and a successful controller get-cluster observation under
the current site authority. Native names or arbitrary UUIDs are not proof.

Revocation withdraws this imported grant. An independent native CRD or Astro
create receipt can still prove ownership. Every credential download continues to
verify live Kubernetes ownership. Re-enrollment after revocation requires a new
successful controller read completed after revocation.`}
	list := &cobra.Command{Use: "list", Short: "List a page of live imported binding metadata", Args: cobra.NoArgs, RunE: nvkeScoped(runNVKEAccessList)}
	nvkeReadSiteFlags(list)
	nvkePageFlags(list)
	enroll := &cobra.Command{Use: "enroll SOURCE_OPERATION_GUID", Short: "Enroll ownership from an exact successful controller cluster read", Args: cobra.ExactArgs(1), RunE: nvkeScoped(runNVKEAccessEnroll)}
	nvkeAccessWriteFlags(enroll)
	enroll.Flags().Int("source-version", 0, "Reviewed successful get-cluster operation version (required)")
	_ = enroll.MarkFlagRequired("source-version")
	revoke := &cobra.Command{Use: "revoke BINDING_GUID", Short: "Withdraw an imported grant at its reviewed version", Args: cobra.ExactArgs(1), RunE: nvkeScoped(runNVKEAccessRevoke)}
	nvkeAccessWriteFlags(revoke)
	revoke.Flags().Int("version", 0, "Reviewed binding version from access list (required)")
	_ = revoke.MarkFlagRequired("version")
	group.AddCommand(list, enroll, revoke)
	return group
}

func nvkeAccessWriteFlags(cmd *cobra.Command) {
	nvkeReadSiteFlags(cmd)
	cmd.Flags().Int("site-version", 0, "Reviewed current site version (required)")
	_ = cmd.MarkFlagRequired("site-version")
}

func runNVKEAccessList(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
	site := resourceStringFlag(cmd, "site")
	if err := nvkeGUID(site); err != nil {
		return err
	}
	after, limit, err := nvkePage(cmd)
	if err != nil {
		return err
	}
	page, err := c.NVKEAccessBindings(ctx, site, after, limit)
	if err != nil {
		return err
	}
	for _, binding := range page.Items {
		if err := validateNVKEAccessBinding(&binding, site); err != nil {
			return err
		}
	}
	return nvkeJSON(cmd, page)
}

func nvkeAccessReview(cmd *cobra.Command, identity, versionFlag string) (string, int, int, error) {
	site := resourceStringFlag(cmd, "site")
	siteVersion, _ := cmd.Flags().GetInt("site-version")
	version, _ := cmd.Flags().GetInt(versionFlag)
	if nvkeGUID(site) != nil || nvkeGUID(identity) != nil || siteVersion < 1 || version < 1 {
		return "", 0, 0, errors.New("use exact nonzero GUIDs and positive reviewed site/source or binding versions")
	}
	return site, siteVersion, version, nil
}

func validateNVKEAccessBinding(binding *api.NVKEAccessBinding, site string) error {
	if binding == nil || binding.SiteID != site || binding.Version < 1 || nvkeGUID(binding.ID) != nil || nvkeGUID(binding.ClusterID) != nil || nvkeGUID(binding.SourceOperationID) != nil || nvkeGUID(binding.ControllerConnectionID) != nil {
		return errors.New("NVKE access binding reply did not confirm the reviewed scope")
	}
	return nil
}

func runNVKEAccessEnroll(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
	site, siteVersion, sourceVersion, err := nvkeAccessReview(cmd, args[0], "source-version")
	if err != nil {
		return err
	}
	_, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("NVKE enrollment caller identity unavailable")
	}
	binding, err := c.EnrollNVKEAccess(ctx, api.NVKEAccessEnrollInput{SiteID: site, ExpectedSiteVersion: siteVersion, ExpectedActorID: actor, SourceOperationID: args[0], ExpectedSourceVersion: sourceVersion})
	if err != nil {
		return fmt.Errorf("%w; inspect access list for this site before retrying the same reviewed observation", err)
	}
	if err = validateNVKEAccessBinding(binding, site); err != nil {
		return err
	}
	if binding.DeletedAt != nil {
		return errors.New("NVKE enrollment did not confirm a live binding")
	}
	return nvkeJSON(cmd, binding)
}

func runNVKEAccessRevoke(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
	site, siteVersion, version, err := nvkeAccessReview(cmd, args[0], "version")
	if err != nil {
		return err
	}
	_, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("NVKE revocation caller identity unavailable")
	}
	binding, err := c.RevokeNVKEAccess(ctx, api.NVKEAccessRevokeInput{SiteID: site, ExpectedSiteVersion: siteVersion, ExpectedActorID: actor, BindingID: args[0], ExpectedVersion: version})
	if err != nil {
		return fmt.Errorf("%w; inspect access list to determine whether the grant is still live", err)
	}
	if err = validateNVKEAccessBinding(binding, site); err != nil {
		return err
	}
	if binding.ID != args[0] || binding.DeletedAt == nil {
		return errors.New("NVKE revocation did not confirm the reviewed binding was withdrawn")
	}
	return nvkeJSON(cmd, binding)
}
