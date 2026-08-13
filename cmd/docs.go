package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/portabledocs"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

const publicDocsBaseURL = "https://astrolift.dev"

var (
	docsExportForce bool
	docsManForce    bool
)

var docsCmd = &cobra.Command{
	Use:   "docs [topic]",
	Short: "Open the platform docs or access the embedded offline reference",
	Long: `Open the public Astrolift documentation in a browser. A topic may be
client, cli, api, mcp, manifest, agents, or workflows.

Use "astro docs show" for release-matched Markdown without a browser,
"astro docs export" for a portable documentation tree, or "astro docs man"
to generate section-1 man pages from this binary's live command tree.`,
	Args:              cobra.MaximumNArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		url := publicDocsBaseURL
		if len(args) == 1 {
			topic, err := portabledocs.Resolve(args[0])
			if err != nil {
				return err
			}
			url += topic.OnlinePath
		}
		return openBrowser(url)
	},
}

var docsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List embedded documentation topics",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		topics := portabledocs.Topics()
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, topics)
		}
		for _, topic := range topics {
			fmt.Fprintf(cmd.OutOrStdout(), "%-12s %s\n", topic.Slug, topic.Title)
		}
		return nil
	},
}

var docsShowCmd = &cobra.Command{
	Use:   "show <topic>",
	Short: "Print one embedded Markdown topic to stdout",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body, _, err := portabledocs.Read(args[0])
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), strings.TrimRight(body, "\n")+"\n")
		return err
	},
}

var docsExportCmd = &cobra.Command{
	Use:   "export [directory]",
	Short: "Export embedded guides, generated command Markdown, and man pages",
	Long: `Build a portable, network-free documentation tree from this binary.
The export contains release-matched topic guides, llms.txt, a Markdown command
reference generated from the live Cobra tree, and section-1 man pages.

Existing files are never overwritten unless --force is supplied.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		destination := "astrolift-docs"
		if len(args) == 1 {
			destination = args[0]
		}
		count, err := exportPortableDocs(destination, docsExportForce)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Exported %d files to %s\n", count, destination)
		return nil
	},
}

var docsManCmd = &cobra.Command{
	Use:   "man [directory]",
	Short: "Generate section-1 man pages from the live command tree",
	Long: `Generate astro.1 and one section-1 page per available subcommand.
The default destination is the current directory. Existing pages are not
overwritten unless --force is supplied.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		destination := "."
		if len(args) == 1 {
			destination = args[0]
		}
		count, err := exportManPages(destination, docsManForce)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Generated %d man pages in %s\n", count, destination)
		return nil
	},
}

func exportPortableDocs(destination string, force bool) (int, error) {
	temporary, err := os.MkdirTemp("", "astro-docs-export-*")
	if err != nil {
		return 0, fmt.Errorf("creating temporary docs tree: %w", err)
	}
	defer os.RemoveAll(temporary)

	guideDir := filepath.Join(temporary, "guides")
	if err := os.MkdirAll(guideDir, 0o755); err != nil {
		return 0, fmt.Errorf("creating guide directory: %w", err)
	}
	files, err := portabledocs.Files()
	if err != nil {
		return 0, err
	}
	for filename, body := range files {
		path := filepath.Join(guideDir, filename)
		if filename == "llms.txt" {
			path = filepath.Join(temporary, filename)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return 0, fmt.Errorf("writing %s: %w", filename, err)
		}
	}

	commandsDir := filepath.Join(temporary, "commands")
	if err := os.MkdirAll(commandsDir, 0o755); err != nil {
		return 0, fmt.Errorf("creating command reference directory: %w", err)
	}
	rootCmd.DisableAutoGenTag = true
	if err := doc.GenMarkdownTree(rootCmd, commandsDir); err != nil {
		return 0, fmt.Errorf("generating command Markdown: %w", err)
	}

	manDir := filepath.Join(temporary, "man", "man1")
	if err := generateManTree(manDir); err != nil {
		return 0, err
	}
	readme := "# Portable Astrolift documentation\n\n" +
		"Start with `llms.txt` or `guides/client.md`. Command reference is in " +
		"`commands/`; section-1 man pages are in `man/man1/`.\n"
	if err := os.WriteFile(filepath.Join(temporary, "README.md"), []byte(readme), 0o644); err != nil {
		return 0, fmt.Errorf("writing export README: %w", err)
	}
	return copyTreeWithoutOverwrite(temporary, destination, force)
}

func exportManPages(destination string, force bool) (int, error) {
	temporary, err := os.MkdirTemp("", "astro-man-export-*")
	if err != nil {
		return 0, fmt.Errorf("creating temporary man tree: %w", err)
	}
	defer os.RemoveAll(temporary)
	if err := generateManTree(temporary); err != nil {
		return 0, err
	}
	return copyTreeWithoutOverwrite(temporary, destination, force)
}

func generateManTree(destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return fmt.Errorf("creating man directory: %w", err)
	}
	rootCmd.DisableAutoGenTag = true
	headerDate := buildDate()
	header := &doc.GenManHeader{
		Section: "1",
		Date:    &headerDate,
		Source:  "Astrolift " + Version,
		Manual:  "Astrolift CLI Manual",
	}
	if err := doc.GenManTree(rootCmd, header, destination); err != nil {
		return fmt.Errorf("generating man pages: %w", err)
	}
	return normalizeManTree(destination, headerDate)
}

// normalizeManTree fixes portability issues in Cobra's generated roff. Its
// month-only date is rejected by mandoc, and go-md2man's flag layout emits
// redundant paragraphs plus tab-indented text. Converting those flag blocks to
// standard tagged paragraphs keeps the pages quiet under mandoc and readable
// across BSD and GNU man implementations.
func normalizeManTree(destination string, generatedAt time.Time) error {
	return filepath.WalkDir(destination, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".1" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading generated man page %s: %w", path, err)
		}
		lines := strings.Split(string(body), "\n")
		normalized := make([]string, 0, len(lines))
		oldDate := `"` + generatedAt.Format("Jan 2006") + `"`
		newDate := `"` + generatedAt.Format("2006-01-02") + `"`
		noFill := false
		for index := 0; index < len(lines); index++ {
			line := lines[index]
			if strings.HasPrefix(line, ".TH ") {
				line = strings.Replace(line, oldDate, newDate, 1)
			}
			if line == ".PP" && index+2 < len(lines) &&
				strings.HasPrefix(lines[index+1], `\fB`) &&
				strings.HasPrefix(lines[index+2], "\t") {
				normalized = append(normalized, ".TP", lines[index+1])
				normalized = appendWrappedRoff(normalized, strings.TrimPrefix(lines[index+2], "\t"), noFill)
				index += 2
				continue
			}
			if line == ".PP" && len(normalized) > 0 && strings.HasPrefix(normalized[len(normalized)-1], ".SH ") {
				continue
			}
			normalized = appendWrappedRoff(normalized, line, noFill)
			if line == ".nf" {
				noFill = true
			} else if line == ".fi" {
				noFill = false
			}
		}
		if err := os.WriteFile(path, []byte(strings.Join(normalized, "\n")), 0o644); err != nil {
			return fmt.Errorf("normalizing generated man page %s: %w", path, err)
		}
		return nil
	})
}

func appendWrappedRoff(lines []string, line string, noFill bool) []string {
	if noFill || strings.HasPrefix(line, ".") {
		return append(lines, line)
	}
	for len(line) > 64 {
		cut := strings.LastIndex(line[:65], " ")
		if cut <= 0 {
			break
		}
		lines = append(lines, strings.TrimRight(line[:cut], " "))
		line = strings.TrimLeft(line[cut+1:], " ")
	}
	return append(lines, line)
}

func buildDate() time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, Date); err == nil {
			return parsed.UTC()
		}
	}
	return time.Now().UTC()
}

func copyTreeWithoutOverwrite(source, destination string, force bool) (int, error) {
	var relativeFiles []string
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		relativeFiles = append(relativeFiles, relative)
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("walking generated documentation: %w", err)
	}
	sort.Strings(relativeFiles)
	if !force {
		for _, relative := range relativeFiles {
			target := filepath.Join(destination, relative)
			if _, err := os.Stat(target); err == nil {
				return 0, fmt.Errorf("refusing to overwrite %s (pass --force to replace generated files)", target)
			} else if !os.IsNotExist(err) {
				return 0, fmt.Errorf("checking %s: %w", target, err)
			}
		}
	}
	for _, relative := range relativeFiles {
		sourcePath := filepath.Join(source, relative)
		target := filepath.Join(destination, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return 0, fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
		}
		body, err := os.ReadFile(sourcePath)
		if err != nil {
			return 0, fmt.Errorf("reading generated %s: %w", relative, err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return 0, fmt.Errorf("writing %s: %w", target, err)
		}
	}
	return len(relativeFiles), nil
}

func init() {
	docsExportCmd.Flags().BoolVar(&docsExportForce, "force", false, "Overwrite generated files that already exist")
	docsManCmd.Flags().BoolVar(&docsManForce, "force", false, "Overwrite generated man pages that already exist")
	docsCmd.AddCommand(docsListCmd, docsShowCmd, docsExportCmd, docsManCmd)
	rootCmd.AddCommand(docsCmd)
}
