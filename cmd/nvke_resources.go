package cmd

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

func nvkeReadSiteFlags(cmd *cobra.Command) {
	cmd.Flags().String("site", "", "Exact site GUID (required)")
	_ = cmd.MarkFlagRequired("site")
}
func nvkeObservation(ctx context.Context, c *api.Client, id, site string, inventory bool) (*api.NVKEOperation, error) {
	if nvkeGUID(id) != nil || nvkeGUID(site) != nil {
		return nil, errors.New("use canonical operation and site GUIDs")
	}
	row, err := c.NVKEOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.SiteID != site || row.Status != "SUCCEEDED" || (inventory && !row.IsInventory) {
		return nil, errors.New("a successful matching saved observation is required")
	}
	return row, nil
}
func nvkePrivateText(cmd *cobra.Command, text string) error {
	filename := resourceStringFlag(cmd, "output")
	if filename == "" {
		return errors.New("--output requires a new private file, or - explicitly for stdout")
	}
	if !utf8.ValidString(text) {
		return errors.New("native text is not valid UTF-8")
	}
	if filename == "-" {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), text)
		return err
	}
	return writeNVKEExclusive(filename, append([]byte(text), '\n'))
}
func newNVKEResourceCommand() *cobra.Command {
	group := &cobra.Command{Use: "resource", Short: "Browse saved inventories and prepare server-bound resource edits"}
	list := &cobra.Command{Use: "list SNAPSHOT_GUID", Short: "Search a server-paged complete saved inventory", Args: cobra.ExactArgs(1), RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
		after, limit, err := nvkePage(cmd)
		if err != nil {
			return err
		}
		if _, err = nvkeObservation(ctx, c, args[0], resourceStringFlag(cmd, "site"), true); err != nil {
			return err
		}
		_, actor, err := reviewedRequestScope(cmd, c)
		if err != nil {
			return errors.New("NVKE read identity unavailable")
		}
		page, err := c.NVKEInventoryPage(ctx, args[0], actor, after, limit, resourceStringFlag(cmd, "search"))
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, page)
	})}
	nvkeReadSiteFlags(list)
	nvkePageFlags(list)
	list.Flags().String("search", "", "Search the complete saved metadata")
	group.AddCommand(list)
	for _, verb := range []string{"get", "document"} {
		selected := verb
		cmd := &cobra.Command{Use: verb + " SNAPSHOT_GUID", Short: verb + " a signed row from a saved inventory", Args: cobra.ExactArgs(1), RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
			site := resourceStringFlag(cmd, "site")
			key := resourceStringFlag(cmd, "resource-key")
			if key == "" || len(key) > 4096 {
				return errors.New("use the signed resource-key from the inventory page")
			}
			if selected == "document" && resourceStringFlag(cmd, "output") == "" {
				return errors.New("explicit --output is required for native documents")
			}
			row, err := nvkeObservation(ctx, c, args[0], site, true)
			if err != nil {
				return err
			}
			_, actor, err := reviewedRequestScope(cmd, c)
			if err != nil {
				return errors.New("NVKE read identity unavailable")
			}
			if selected == "document" {
				text, err := c.NVKEInventoryDocument(ctx, args[0], actor, key)
				if err != nil {
					return err
				}
				if err = nvkePrivateText(cmd, text); err != nil {
					return err
				}
				if resourceStringFlag(cmd, "output") != "-" {
					return nvkeJSON(cmd, map[string]bool{"saved": true})
				}
				return nil
			}
			result, err := c.NVKEInventoryResource(ctx, args[0], actor, key)
			if err != nil {
				return err
			}
			if result.SiteID != site || result.OperationVersion != row.Version {
				return errors.New("resource observation no longer matches its reviewed site/version")
			}
			return nvkeJSON(cmd, result)
		})}
		nvkeReadSiteFlags(cmd)
		cmd.Flags().String("resource-key", "", "Signed inventory row key (required)")
		_ = cmd.MarkFlagRequired("resource-key")
		if selected == "document" {
			cmd.Flags().String("output", "", "New private native JSON file, or - explicitly for stdout")
		}
		group.AddCommand(cmd)
	}
	editor := &cobra.Command{Use: "editor ACTION_KEY", Short: "Load a sensitive edit document from an exact reviewed source", Args: cobra.ExactArgs(1), RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
		site := resourceStringFlag(cmd, "site")
		if nvkeGUID(site) != nil {
			return errors.New("use a canonical site GUID")
		}
		source, err := nvkeSourceFromFlags(cmd)
		if err != nil {
			return err
		}
		if source == nil {
			return errors.New("source-operation and source-version are required")
		}
		if resourceStringFlag(cmd, "output") == "" {
			return errors.New("explicit --output is required for sensitive editor text")
		}
		_, actor, err := reviewedRequestScope(cmd, c)
		if err != nil {
			return errors.New("NVKE read identity unavailable")
		}
		result, err := c.NVKEResourceEditor(ctx, site, actor, args[0], *source)
		if err != nil {
			return err
		}
		if result.OperationKey != args[0] || !sameNVKESource(&result.Source, source) || (result.Concurrency != "native_version" && result.Concurrency != "observed_only") {
			return errors.New("editor reply did not confirm the reviewed source")
		}
		if result.ParametersText != nil {
			if err = nvkePrivateText(cmd, *result.ParametersText); err != nil {
				return err
			}
			if resourceStringFlag(cmd, "output") == "-" {
				fmt.Fprintln(cmd.ErrOrStderr(), "Concurrency: "+result.Concurrency)
				return nil
			}
			result.ParametersText = nil
		}
		return nvkeJSON(cmd, result)
	})}
	nvkeReadSiteFlags(editor)
	nvkeSourceFlags(editor)
	editor.Flags().String("output", "", "New private edit document, or - explicitly for stdout")
	group.AddCommand(editor)
	return group
}
func newNVKEDiagnosticsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "diagnostics OPERATION_GUID", Short: "Read a page of typed historical control-plane/history/SPIRE diagnostics", Args: cobra.ExactArgs(1), RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
		site := resourceStringFlag(cmd, "site")
		version, _ := cmd.Flags().GetInt("version")
		if nvkeGUID(args[0]) != nil || nvkeGUID(site) != nil || version < 1 {
			return errors.New("use exact site/operation GUIDs and a positive reviewed version")
		}
		after, limit, err := nvkePage(cmd)
		if err != nil {
			return err
		}
		_, actor, err := reviewedRequestScope(cmd, c)
		if err != nil {
			return errors.New("NVKE read identity unavailable")
		}
		page, err := c.NVKEDiagnosticPage(ctx, site, args[0], actor, version, after, limit, resourceStringFlag(cmd, "search"))
		if err != nil {
			return err
		}
		if nvkeGUID(page.Summary.ClusterID) != nil {
			return errors.New("diagnostic cluster identity is unavailable")
		}
		return nvkeJSON(cmd, page)
	})}
	nvkeReadSiteFlags(cmd)
	cmd.Flags().Int("version", 0, "Exact successful source operation version (required)")
	_ = cmd.MarkFlagRequired("version")
	nvkePageFlags(cmd)
	cmd.Flags().String("search", "", "Search safe saved diagnostic metadata")
	return cmd
}
func newNVKERecoverCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "recover", Short: "Look up the original receipt without native input or re-admission", Args: cobra.NoArgs, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
		saved, err := readReviewedRequest(resourceStringFlag(cmd, "request-file"))
		if err != nil {
			return errors.New("original private recovery file unavailable")
		}
		if saved.Kind == nvkeRequestKind {
			err = validateNVKERecovery(saved)
		} else {
			err = validateNVKETextRecovery(saved)
		}
		if err != nil {
			return err
		}
		server, actor, err := reviewedRequestScope(cmd, c)
		if err != nil {
			return errors.New("NVKE caller identity unavailable")
		}
		if err = saved.checkScope(saved.Kind, server, c.Org(), actor); err != nil {
			return err
		}
		row, err := c.NVKERequest(ctx, saved.TargetID, saved.RequestID)
		if err != nil {
			return err
		}
		if row == nil {
			return errors.New("no original receipt is visible; this does not prove no effect; retry the original command and text with this recovery file")
		}
		if err = validateNVKEReceipt(saved, row); err != nil {
			return err
		}
		return finishNVKERequest(cmd, ctx, c, row)
	})}
	cmd.Flags().String("request-file", "", "Original private recovery file (required)")
	_ = cmd.MarkFlagRequired("request-file")
	cmd.Flags().Bool("wait", false, "Wait on the original operation after receipt recovery")
	nvkeWaitFlags(cmd)
	return cmd
}
