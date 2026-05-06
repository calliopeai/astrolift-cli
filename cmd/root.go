package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Version is set at build time via -ldflags.
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "astro",
	Short: "Astrolift CLI",
	Long:  "The developer CLI for the Astrolift platform.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return initConfig()
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().String("api-url", "", "Astrolift API URL")
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
		fmt.Fprintf(os.Stdout, "astro %s\n", Version)
	},
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
