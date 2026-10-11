package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type reviewedClusterSelector struct {
	slug string
	id   string
}

func optionalReviewedClusterID(cmd *cobra.Command) (string, error) {
	id, _ := cmd.Flags().GetString("cluster-id")
	id = strings.TrimSpace(id)
	if id == "" {
		if cmd.Flags().Changed("cluster-id") {
			return "", errors.New("--cluster-id must be a canonical nonzero UUID")
		}
		return "", nil
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return "", errors.New("--cluster-id must be a canonical nonzero UUID")
	}
	return id, nil
}

func parseReviewedClusterSelector(cmd *cobra.Command, slug string) (reviewedClusterSelector, error) {
	id, err := optionalReviewedClusterID(cmd)
	if err != nil {
		return reviewedClusterSelector{}, err
	}
	slug = strings.TrimSpace(slug)
	if slug != "" && id != "" {
		return reviewedClusterSelector{}, errors.New("--slug and --cluster-id are mutually exclusive")
	}
	if slug == "" && id == "" {
		return reviewedClusterSelector{}, errors.New("exactly one of --slug or --cluster-id is required")
	}
	return reviewedClusterSelector{slug: slug, id: id}, nil
}

func (s reviewedClusterSelector) targetName() string {
	if s.id != "" {
		return s.id
	}
	return s.slug
}

func (s reviewedClusterSelector) matches(targetID, targetName string) bool {
	if s.id != "" {
		return s.id == targetID
	}
	return s.slug == targetName
}

func (s reviewedClusterSelector) resolve(ctx context.Context, client *api.Client) (string, error) {
	if s.id != "" {
		return s.id, nil
	}
	cluster, err := fetchClusterBySlug(ctx, client, s.slug)
	if err != nil {
		return "", fmt.Errorf("slug discovery requires cluster.register inventory access and is unavailable; use --cluster-id with a known GUID and authorized cluster.manage access: %w", err)
	}
	if !collectorExactUUID(cluster.ID) {
		return "", errors.New("cluster discovery returned an invalid canonical nonzero GUID")
	}
	return cluster.ID, nil
}

func reviewedClusterSelectorArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return err
	}
	slug, _ := cmd.Flags().GetString("slug")
	_, err := parseReviewedClusterSelector(cmd, slug)
	return err
}

func reviewedClusterStatusArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return err
	}
	_, err := optionalReviewedClusterID(cmd)
	return err
}

func addReviewedClusterSelectorFlags(cmd *cobra.Command) {
	cmd.Flags().String("cluster-id", "", "Known canonical cluster UUID; use instead of --slug to avoid operator inventory discovery")
	cmd.MarkFlagsMutuallyExclusive("slug", "cluster-id")
	cmd.MarkFlagsOneRequired("slug", "cluster-id")
}
