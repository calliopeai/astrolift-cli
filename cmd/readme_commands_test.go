package cmd

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

var (
	readmeInlineAstro = regexp.MustCompile("`(astro [^`]+)`")
	readmeCommandWord = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	readmeFlag        = regexp.MustCompile(`^--([a-z][a-z0-9-]*)`)
)

// readmeAstroInvocations returns every `astro ...` invocation the README
// shows: lines in fenced code blocks and code spans, comments included.
func readmeAstroInvocations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	fenced := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			code, _, _ := strings.Cut(line, "#")
			if s := strings.TrimSpace(code); strings.HasPrefix(s, "astro ") {
				found = append(found, s)
			}
		}
		for _, m := range readmeInlineAstro.FindAllStringSubmatch(line, -1) {
			found = append(found, m[1])
		}
	}
	return found
}

// TestReadmeCommandsExist keeps the README honest about the binary (#159):
// every command it shows must be a usable command of this build, and every
// flag it passes must be one that command accepts.
func TestReadmeCommandsExist(t *testing.T) {
	invocations := readmeAstroInvocations(t)
	if len(invocations) < 20 {
		t.Fatalf("found only %d astro invocations in README.md", len(invocations))
	}
	for _, line := range invocations {
		tokens := strings.Fields(line)[1:]
		var path []string
		for i := 0; i < len(tokens); i++ {
			tok := tokens[i]
			if m := readmeFlag.FindStringSubmatch(tok); m != nil {
				// A global flag ahead of the command words, with its value.
				if f := rootCmd.PersistentFlags().Lookup(m[1]); f != nil && len(path) == 0 {
					if f.Value.Type() != "bool" && !strings.Contains(tok, "=") {
						i++
					}
					continue
				}
			}
			if !readmeCommandWord.MatchString(tok) {
				break
			}
			path = append(path, tok)
		}
		found, rest, err := rootCmd.Find(path)
		if err != nil {
			t.Errorf("README shows %q: %v", line, err)
			continue
		}
		if !found.Runnable() && !found.HasAvailableSubCommands() {
			t.Errorf("README shows %q, but %q is not a usable command", line, found.CommandPath())
			continue
		}
		if !found.Runnable() && len(rest) > 0 {
			t.Errorf("README shows %q, but %q has no subcommand %q", line, found.CommandPath(), rest[0])
			continue
		}
		checkReadmeFlags(t, line, found)
	}
}

func checkReadmeFlags(t *testing.T, line string, c *cobra.Command) {
	t.Helper()
	for _, tok := range strings.Fields(line) {
		if tok == "--" {
			return // the rest is the remote command's argv
		}
		m := readmeFlag.FindStringSubmatch(tok)
		if m == nil || m[1] == "help" {
			continue
		}
		if c.Flag(m[1]) == nil && rootCmd.PersistentFlags().Lookup(m[1]) == nil {
			t.Errorf("README shows %q, but %q has no --%s flag", line, c.CommandPath(), m[1])
		}
	}
}
