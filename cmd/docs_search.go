package cmd

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/calliopeai/astrolift-cli/internal/portabledocs"
	"github.com/spf13/cobra"
)

type docsSearchMatch struct {
	Topic   portabledocs.Topic `json:"topic"`
	Line    int                `json:"line"`
	Snippet string             `json:"snippet"`
}

type docsSearchResult struct {
	Matches      []docsSearchMatch `json:"matches"`
	TotalMatches int               `json:"totalMatches"`
	Truncated    bool              `json:"truncated"`
}

// Quotes group a contiguous phrase; unquoted terms may occur separately.
// Matching uses normalized whitespace, case-insensitive substrings, and AND.
func parseDocsSearchQuery(query string) ([]string, error) {
	if len(query) > 512 || !utf8.ValidString(query) {
		return nil, errors.New("search query must be valid UTF-8 and at most 512 bytes")
	}
	terms := make([]string, 0)
	var token strings.Builder
	quoted := false
	flush := func() {
		term := strings.ToLower(strings.Join(strings.Fields(token.String()), " "))
		if term != "" {
			terms = append(terms, term)
		}
		token.Reset()
	}
	for _, r := range query {
		if r == '"' {
			flush()
			quoted = !quoted
		} else if unicode.IsSpace(r) && !quoted {
			flush()
		} else {
			token.WriteRune(r)
		}
	}
	if quoted {
		return nil, errors.New("search query has an unmatched double quote")
	}
	flush()
	if len(terms) == 0 || len(terms) > 32 {
		return nil, errors.New("search query must contain between 1 and 32 terms or quoted phrases")
	}
	return terms, nil
}

func docsSearchContains(text string, terms []string) bool {
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	for _, term := range terms {
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}

func docsSearchSnippet(body string, terms []string) (int, string) {
	lines := strings.Split(body, "\n")
	lineNumber, snippet := 0, ""
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if snippet == "" {
			lineNumber, snippet = i+1, line
		}
		for _, term := range terms {
			// A multi-line phrase is searchable; its first word locates a
			// useful source line even when the full phrase wraps.
			first := strings.Fields(term)[0]
			if docsSearchContains(line, []string{first}) {
				lineNumber, snippet = i+1, line
				goto found
			}
		}
	}
found:
	runes := []rune(snippet)
	if len(runes) > 180 {
		snippet = string(runes[:177]) + "..."
	}
	return lineNumber, snippet
}

func searchPortableDocs(query string, limit int) (*docsSearchResult, error) {
	if limit < 1 || limit > 50 {
		return nil, errors.New("--limit must be between 1 and 50")
	}
	terms, err := parseDocsSearchQuery(query)
	if err != nil {
		return nil, err
	}
	result := &docsSearchResult{Matches: make([]docsSearchMatch, 0)}
	// Topic catalogue order is deterministic and useful for the normal
	// reading sequence. Only the binary's immutable public guides are read.
	for _, topic := range portabledocs.Topics() {
		body, _, err := portabledocs.Read(topic.Slug)
		if err != nil {
			return nil, err
		}
		if !docsSearchContains(topic.Slug+" "+topic.Title+" "+body, terms) {
			continue
		}
		result.TotalMatches++
		if len(result.Matches) >= limit {
			continue
		}
		line, snippet := docsSearchSnippet(body, terms)
		result.Matches = append(result.Matches, docsSearchMatch{Topic: topic, Line: line, Snippet: snippet})
	}
	result.Truncated = result.TotalMatches > len(result.Matches)
	return result, nil
}

func runDocsSearch(cmd *cobra.Command, query string) error {
	limit, _ := cmd.Flags().GetInt("limit")
	result, err := searchPortableDocs(query, limit)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, result)
	}
	for _, match := range result.Matches {
		fmt.Fprintf(cmd.OutOrStdout(), "%s — %s (line %d)\n  %s\n", match.Topic.Slug, match.Topic.Title, match.Line, match.Snippet)
	}
	if result.TotalMatches == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No embedded guide matches.")
	}
	if result.Truncated {
		fmt.Fprintf(cmd.OutOrStdout(), "Showing %d of %d matching guides; increase --limit or refine the query.\n", len(result.Matches), result.TotalMatches)
	}
	return nil
}

var docsSearchCmd = &cobra.Command{
	Use: "search <query>", Short: "Search release-matched embedded guides without network or login",
	Long: `Search the public guides embedded in this binary, without network access,
credentials or a configured server. Matching is case-insensitive: all whitespace-
separated terms must occur in the guide or its topic/title. Double quotes group
a contiguous phrase; whitespace inside a phrase is normalized. Results follow
the topic catalogue order, with one source line and a snippet of at most 180
characters per matching guide. --limit bounds results (default 10, maximum 50).

Examples:
  astro docs search 'workflow recovery'
  astro docs search '"request file"' --json
  astro docs show reviewed-starts`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error { return runDocsSearch(cmd, args[0]) },
}

func init() {
	docsCmd.AddCommand(docsSearchCmd)
	docsSearchCmd.Flags().Int("limit", 10, "Maximum matching guides (1–50)")
}
