package portabledocs

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

var inlineLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// PortableMarkdown keeps links between embedded guides navigable after export
// or onboarding. Unbundled pages link to the public site; canonical source bytes
// remain untouched for the cross-repository snapshot comparison.
func PortableMarkdown(topic Topic, body string) string {
	source := strings.Trim(topic.OnlinePath, "/") + ".md"
	bundled := make(map[string]string, len(topics))
	for _, candidate := range topics {
		bundled[strings.Trim(candidate.OnlinePath, "/")+".md"] = candidate.Filename
	}
	lines := strings.Split(body, "\n")
	fence := ""
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			delimiter := trimmed[:3]
			if fence == "" {
				fence = delimiter
			} else if delimiter == fence {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		lines[index] = inlineLink.ReplaceAllStringFunc(line, func(match string) string {
			raw := match[2 : len(match)-1]
			link, err := url.Parse(raw)
			if err != nil || link.IsAbs() || link.Host != "" || link.Path == "" {
				return match
			}
			target := path.Clean(path.Join(path.Dir(source), link.Path))
			if strings.HasPrefix(link.Path, "/") {
				target = path.Clean(strings.TrimPrefix(link.Path, "/"))
			}
			if filename, ok := bundled[target]; ok {
				link.Path = filename
				return "](" + link.String() + ")"
			}
			if target == "index.md" {
				target = ""
			} else if strings.HasSuffix(target, "/index.md") {
				target = strings.TrimSuffix(target, "index.md")
			} else if strings.HasSuffix(target, ".md") {
				target = strings.TrimSuffix(target, ".md") + "/"
			}
			link.Scheme = "https"
			link.Host = "astrolift.dev"
			link.Path = "/" + strings.TrimPrefix(target, "/")
			return "](" + link.String() + ")"
		})
	}
	return strings.Join(lines, "\n")
}
