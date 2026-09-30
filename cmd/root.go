package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Version, Commit, and Date are injected at build time via -ldflags (see
// .goreleaser.yaml, the Makefile, and Dockerfile.source). Plain `go build` /
// `go install` source builds keep these defaults, so a released binary is
// always distinguishable from an un-stamped source build.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "astro",
	Short: "Astrolift CLI",
	Long:  "The developer CLI for the Astrolift platform.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return initConfig()
	},
	// Advisory staleness hint (#46). Cobra skips PostRun when the command
	// returned an error, which is what we want: never pile a version notice
	// on top of a real failure.
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		maybeNotifyStale(cmd)
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().String("api-url", "", "Astrolift API URL")
	rootCmd.PersistentFlags().String("server", "", "Registered server for this command (does not change the saved selection)")
	rootCmd.PersistentFlags().String("token", "", "API token (overrides stored credentials)")
	rootCmd.PersistentFlags().String("org", "", "Organization slug")
	rootCmd.PersistentFlags().String("team", "", "Team slug")
	rootCmd.PersistentFlags().String("project", "", "Project slug")
	rootCmd.PersistentFlags().String("app", "", "App slug")
	rootCmd.PersistentFlags().Bool("json", false, "Output as JSON")
	rootCmd.PersistentFlags().Bool("no-color", false, "Disable colored output")
	rootCmd.PersistentFlags().Bool("no-prompt", false, "Disable interactive prompts")
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug output")

	_ = viper.BindPFlag("api_url", rootCmd.PersistentFlags().Lookup("api-url"))
	_ = viper.BindPFlag("server", rootCmd.PersistentFlags().Lookup("server"))
	_ = viper.BindPFlag("token", rootCmd.PersistentFlags().Lookup("token"))
	_ = viper.BindPFlag("default_org", rootCmd.PersistentFlags().Lookup("org"))
	_ = viper.BindPFlag("output_json", rootCmd.PersistentFlags().Lookup("json"))
	_ = viper.BindPFlag("no_color", rootCmd.PersistentFlags().Lookup("no-color"))
	_ = viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))

	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the CLI version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), versionString())
	},
}

// versionString is the single-line build stamp. Commit and Date make binary
// staleness visible even when the version tag alone is ambiguous.
func versionString() string {
	return fmt.Sprintf("astro %s (commit %s, built %s)", Version, Commit, Date)
}

func initConfig() error {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("$HOME/.config/astrolift")

	viper.SetEnvPrefix("ASTROLIFT")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("reading config: %w", err)
		}
	}

	return nil
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
