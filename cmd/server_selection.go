package cmd

import (
	"fmt"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/viper"
)

// selectedServer pairs a registered endpoint with its credential namespace.
// It never changes cfg: commands such as org use save that same config later.
func selectedServer(cfg *config.Config, args []string) (string, config.ServerEntry, error) {
	want := strings.TrimSpace(viper.GetString("server"))
	slug := want
	if len(args) > 0 {
		if want != "" && want != args[0] {
			return "", config.ServerEntry{}, fmt.Errorf("server argument %q conflicts with --server %q", args[0], want)
		}
		slug = args[0]
	}
	if slug == "" {
		slug = cfg.CurrentServer
	}
	entry, ok := cfg.Servers[slug]
	if !ok {
		return "", config.ServerEntry{}, fmt.Errorf("server %q not registered; run `astro server add` first", slug)
	}
	if want != "" {
		override := strings.TrimSpace(viper.GetString("api_url"))
		if override != "" && strings.TrimRight(override, "/") != strings.TrimRight(entry.APIURL, "/") {
			return "", config.ServerEntry{}, fmt.Errorf("API URL override does not match registered server %q", slug)
		}
	}
	return slug, entry, nil
}
