package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// apiCmd is the top-level `astro api` group: direct access to the
// platform's own API for the surfaces the CLI has no verb for yet.
var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Call the platform API directly",
	Long: `Send a request to the platform API using your existing session.

Every operator task the CLI has no verb for -- secret bundles, cluster
auth config, manifest promotion -- is reachable through the GraphQL API,
and reaching it by hand means managing the bearer token, the organization
header and the endpoint path yourself. These commands do that part.`,
}

var apiGraphQLCmd = &cobra.Command{
	Use:   "graphql",
	Short: "Run a GraphQL query or mutation against the platform",
	Long: `Run a GraphQL document against the platform API with the current
session's credentials and organization.

The document comes from --file, --query, or standard input. Variables come
from --var name=value (repeatable, values parsed as JSON when they parse and
kept as strings otherwise) and --vars-file (a JSON object, merged first so
individual --var flags win).

The response's ` + "`data`" + ` is printed as indented JSON. GraphQL errors are
reported on stderr and exit non-zero, so a failed mutation in a script fails
the script.

Examples:
  astro api graphql --file ./attach-bundle.graphql --var appSlug=quake-dash
  astro api graphql -q 'query { astroliftSecretBundles { slug } }'
  cat query.graphql | astro api graphql`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		document, err := readGraphQLDocument(cmd)
		if err != nil {
			return err
		}
		variables, err := graphQLVariables(cmd)
		if err != nil {
			return err
		}

		debug, _ := cmd.Flags().GetBool("debug")
		client, _, _, err := loadActiveClient(ctx, debug)
		if err != nil {
			return err
		}

		// The response shape is whatever the caller asked for, so decode
		// into a generic map and print it rather than pretending to know.
		var data map[string]interface{}
		if err := client.GraphQL(ctx, document, variables, &data); err != nil {
			return err
		}
		return renderJSON(cmd, data)
	},
}

// readGraphQLDocument resolves the document from --file, --query or stdin,
// in that order. Exactly one source may be given.
func readGraphQLDocument(cmd *cobra.Command) (string, error) {
	file, _ := cmd.Flags().GetString("file")
	query, _ := cmd.Flags().GetString("query")
	if file != "" && query != "" {
		return "", fmt.Errorf("pass --file or --query, not both")
	}

	switch {
	case file != "":
		body, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", file, err)
		}
		return strings.TrimSpace(string(body)), nil
	case query != "":
		return strings.TrimSpace(query), nil
	}

	// Stdin only when something is actually piped in: a bare `astro api
	// graphql` on a terminal should say what it wants, not hang waiting
	// for input with no prompt.
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		return "", fmt.Errorf("no GraphQL document: pass --file, --query, or pipe one in on stdin")
	}
	body, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}
	document := strings.TrimSpace(string(body))
	if document == "" {
		return "", fmt.Errorf("no GraphQL document: pass --file, --query, or pipe one in on stdin")
	}
	return document, nil
}

// graphQLVariables merges --vars-file (a JSON object) with repeated
// --var name=value flags. Individual flags win on a collision.
func graphQLVariables(cmd *cobra.Command) (map[string]interface{}, error) {
	variables := map[string]interface{}{}

	varsFile, _ := cmd.Flags().GetString("vars-file")
	if varsFile != "" {
		body, err := os.ReadFile(varsFile)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", varsFile, err)
		}
		if err := json.Unmarshal(body, &variables); err != nil {
			return nil, fmt.Errorf("%s must contain a JSON object of variables: %w", varsFile, err)
		}
	}

	pairs, _ := cmd.Flags().GetStringArray("var")
	for _, pair := range pairs {
		name, raw, found := strings.Cut(pair, "=")
		if !found || name == "" {
			return nil, fmt.Errorf("--var must be name=value, got %q", pair)
		}
		// A value that parses as JSON is passed as that type, so
		// `--var limit=10` and `--var force=true` are a number and a
		// boolean; anything else rides as a string, which is what a
		// slug or a hostname needs.
		var parsed interface{}
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			variables[name] = parsed
		} else {
			variables[name] = raw
		}
	}
	return variables, nil
}

func init() {
	apiGraphQLCmd.Flags().StringP("file", "f", "", "Path to a file holding the GraphQL document")
	apiGraphQLCmd.Flags().StringP("query", "q", "", "GraphQL document as a literal string")
	apiGraphQLCmd.Flags().StringArray("var", nil, "Variable as name=value (repeatable)")
	apiGraphQLCmd.Flags().String("vars-file", "", "Path to a JSON object of variables")

	apiCmd.AddCommand(apiGraphQLCmd)
	rootCmd.AddCommand(apiCmd)
}
