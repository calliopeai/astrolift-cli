BINARY   := astro
MODULE   := github.com/calliopeai/astrolift-cli
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -ldflags "-X $(MODULE)/cmd.Version=$(VERSION) -X $(MODULE)/cmd.Commit=$(COMMIT) -X $(MODULE)/cmd.Date=$(DATE)"

# Vendored chart source — refreshed by `make vendor-charts` before build.
# The metarepo path is used in dev; CI checks the committed copy and skips
# vendor-charts via SKIP_VENDOR_CHARTS=1 (the chart lives in-repo so the
# binary can be built without the metarepo sibling on disk).
PREREQS_SRC ?= ../astrolift-opscode/helm/astrolift-prereqs
PREREQS_DST := internal/charts/astrolift-prereqs
SKILLS_SRC ?= ../astrolift-skills
SKILLS_DST := internal/skills/catalogue

# Canonical public documentation lives in astrolift-docs. A deliberately
# small release-matched snapshot is committed here so `astro docs` and the
# generated man pages work without a network connection or sibling checkout.
DOCS_SRC ?= ../astrolift-docs/docs
DOCS_DST := internal/portabledocs/content

.PHONY: build test fmt lint clean docs manpages vendor-docs vendor-docs-check vendor-charts vendor-charts-check vendor-skills vendor-skills-check

build:
	go build $(LDFLAGS) -o $(BINARY) .

test:
	go test ./... -v -count=1

fmt:
	gofmt -w .
	goimports -w .

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY)
	go clean -testcache

docs: build
	./$(BINARY) docs export build/docs --force

manpages: build
	./$(BINARY) docs man build/man/man1 --force

# Refresh the offline snapshot from the public docs repository. The explicit
# mapping is the contract: do not embed the entire website in the CLI.
vendor-docs:
	@test -d "$(DOCS_SRC)" || (echo "missing $(DOCS_SRC)" && exit 1)
	@mkdir -p "$(DOCS_DST)"
	cp "$(DOCS_SRC)/getting-started.md" "$(DOCS_DST)/start.md"
	cp "$(DOCS_SRC)/guides/clients.md" "$(DOCS_DST)/client.md"
	cp "$(DOCS_SRC)/reference/cli.md" "$(DOCS_DST)/cli.md"
	cp "$(DOCS_SRC)/reference/api.md" "$(DOCS_DST)/api.md"
	cp "$(DOCS_SRC)/reference/mcp.md" "$(DOCS_DST)/mcp.md"
	cp "$(DOCS_SRC)/reference/astrolift-toml.md" "$(DOCS_DST)/manifest.md"
	cp "$(DOCS_SRC)/reference/agent-packages.md" "$(DOCS_DST)/agents.md"
	cp "$(DOCS_SRC)/reference/workflow-toml.md" "$(DOCS_DST)/workflows.md"
	cp "$(DOCS_SRC)/llms.txt" "$(DOCS_DST)/llms.txt"

vendor-docs-check:
	@for file in start.md client.md cli.md api.md mcp.md manifest.md agents.md workflows.md llms.txt; do \
		test -s "$(DOCS_DST)/$$file" || { echo "missing $(DOCS_DST)/$$file — run \`make vendor-docs\`"; exit 1; }; \
	done
	@if [ -d "$(DOCS_SRC)" ]; then \
		for pair in \
			"getting-started.md:start.md" \
			"guides/clients.md:client.md" \
			"reference/cli.md:cli.md" \
			"reference/api.md:api.md" \
			"reference/mcp.md:mcp.md" \
			"reference/astrolift-toml.md:manifest.md" \
			"reference/agent-packages.md:agents.md" \
			"reference/workflow-toml.md:workflows.md" \
			"llms.txt:llms.txt"; do \
			source="$${pair%%:*}"; destination="$${pair#*:}"; \
			cmp -s "$(DOCS_SRC)/$$source" "$(DOCS_DST)/$$destination" || { \
				echo "stale $(DOCS_DST)/$$destination — run \`make vendor-docs\`"; \
				exit 1; \
			}; \
		done; \
	fi

# Refresh the vendored astrolift-prereqs chart from the metarepo. Run this
# whenever the source chart bumps; commit the result. Skips silently when
# the source dir isn't present (CI / clones without the metarepo sibling).
vendor-charts:
	@if [ -d "$(PREREQS_SRC)" ]; then \
		echo "vendoring $(PREREQS_SRC) -> $(PREREQS_DST)"; \
		rm -rf $(PREREQS_DST); \
		mkdir -p $(PREREQS_DST); \
		cp -R $(PREREQS_SRC)/. $(PREREQS_DST)/; \
	else \
		echo "skip vendor-charts: $(PREREQS_SRC) not present (using committed copy)"; \
	fi

# Refresh the vendored agent skills from the metarepo sibling. Bundled into
# the binary so `astro onboard` can install them with no network and no
# access to a private repo -- an agent onboarding itself has neither.
vendor-skills:
	@if [ -d "$(SKILLS_SRC)" ]; then \
		echo "vendoring $(SKILLS_SRC) -> $(SKILLS_DST)"; \
		rm -rf $(SKILLS_DST); \
		mkdir -p $(SKILLS_DST); \
		cp "$(SKILLS_SRC)/catalogue.json" "$(SKILLS_DST)/catalogue.json"; \
		cp -R "$(SKILLS_SRC)/skills" "$(SKILLS_DST)/skills"; \
	else \
		echo "skip vendor-skills: $(SKILLS_SRC) not present (using committed copy)"; \
	fi

vendor-skills-check:
	@test -s "$(SKILLS_DST)/catalogue.json" || { echo "missing $(SKILLS_DST)/catalogue.json — run \`make vendor-skills\`"; exit 1; }
	@if [ -d "$(SKILLS_SRC)" ]; then \
		cmp -s "$(SKILLS_SRC)/catalogue.json" "$(SKILLS_DST)/catalogue.json" || { \
			echo "stale $(SKILLS_DST)/catalogue.json — run \`make vendor-skills\`"; exit 1; }; \
		diff -rq "$(SKILLS_SRC)/skills" "$(SKILLS_DST)/skills" >/dev/null || { \
			echo "stale $(SKILLS_DST)/skills — run \`make vendor-skills\`"; exit 1; }; \
	fi

# CI sanity check — the committed copy must exist and contain Chart.yaml.
vendor-charts-check:
	@test -f $(PREREQS_DST)/Chart.yaml || (echo "missing $(PREREQS_DST)/Chart.yaml — run \`make vendor-charts\`" && exit 1)
