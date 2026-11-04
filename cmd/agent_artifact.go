// Package cmd: `astro agent artifact <task-id> <name>`.
//
// One artifact a run attached to its result (platform #2488), read through
// agentTaskArtifact(orgId, taskId, name). `astro agent inspect` lists a task's
// artifacts; this prints or saves one after checking its content against the
// SHA-256 the server reports.
package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

var agentArtifactOutput string

const agentTaskArtifactQuery = `query($orgId: ID!, $taskId: ID!, $name: String!) {
  agentTaskArtifact(orgId: $orgId, taskId: $taskId, name: $name) {
    name
    kind
    mediaType
    sizeBytes
    sha256
    createdAt
    content
  }
}`

// agentTaskArtifactContent mirrors AstroliftAgentTaskArtifactContent.
type agentTaskArtifactContent struct {
	agentTaskArtifact
	Content string `json:"content"`
}

var agentArtifactCmd = &cobra.Command{
	Use:   "artifact <task-id> <name>",
	Short: "Print or save one artifact a task attached to its result",
	Long: `Reads one artifact a run attached to its result (a JUnit report, coverage,
a conformance report or another text file) through the agentTaskArtifact
GraphQL query and prints its content as stored. 'astro agent inspect' lists
a task's artifacts by name.

The content is checked against the SHA-256 the server reports before
anything is printed or written; a mismatch prints nothing and exits 1.
-o writes the content to a file with mode 0600 instead of standard output.
--json prints the artifact record with its content, or with -o the file
it wrote.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, err := loadScopedAgentClient(cmd)
		if err != nil {
			return err
		}
		return runAgentArtifact(cmd, cmd.Context(), client, args[0], args[1], agentArtifactOutput)
	},
}

func runAgentArtifact(cmd *cobra.Command, ctx context.Context, client *api.Client, taskID, name, output string) error {
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Artifact *agentTaskArtifactContent `json:"agentTaskArtifact"`
	}
	vars := map[string]interface{}{"orgId": client.Org(), "taskId": taskID, "name": name}
	if err := client.GraphQL(readCtx, agentTaskArtifactQuery, vars, &resp); err != nil {
		if errors.Is(err, api.ErrSchemaMismatch) && strings.Contains(strings.ToLower(err.Error()), "agenttaskartifact") {
			return errors.New("this server is older than platform #2488 and keeps no task artifacts")
		}
		return fmt.Errorf("reading artifact: %w", err)
	}
	a := resp.Artifact
	if a == nil {
		return fmt.Errorf("task %s has no artifact %q; `astro agent inspect %s` lists its artifacts", taskID, name, taskID)
	}
	sum := sha256.Sum256([]byte(a.Content))
	if got := hex.EncodeToString(sum[:]); got != a.SHA256 {
		return fmt.Errorf("artifact %s does not match its sha256 (server %s, content %s); nothing was printed or written", a.Name, a.SHA256, got)
	}

	if output == "" {
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, a)
		}
		_, err := io.WriteString(cmd.OutOrStdout(), a.Content)
		return err
	}
	if err := privatefile.Write(output, []byte(a.Content)); err != nil {
		return fmt.Errorf("writing %s: %w", output, err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, struct {
			agentTaskArtifact
			Path string `json:"path"`
		}{a.agentTaskArtifact, output})
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s (%d bytes, sha256 %s) to %s\n", a.Name, len(a.Content), a.SHA256, output)
	return nil
}

func init() {
	agentArtifactCmd.Flags().StringVarP(&agentArtifactOutput, "output", "o", "", "Write the content to this file (mode 0600) instead of standard output")
	agentCmd.AddCommand(agentArtifactCmd)
}
