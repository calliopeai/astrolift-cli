BINARY   := astro
MODULE   := github.com/calliopeai/astrolift-cli
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -ldflags "-X $(MODULE)/cmd.Version=$(VERSION) -X $(MODULE)/cmd.Commit=$(COMMIT) -X $(MODULE)/cmd.Date=$(DATE)"

# Canonical source is opscode. Refresh requires its full published revision;
# builds/checks use the committed offline inventory, never sibling contents.
PREREQS_REPO ?= ../astrolift-opscode
PREREQS_REV ?=
SKILLS_SRC ?= ../astrolift-skills
SKILLS_DST := internal/skills/catalogue

# Canonical public documentation lives in astrolift-docs. A deliberately
# small release-matched snapshot is committed here so `astro docs` and the
# generated man pages work without a network connection or sibling checkout.
DOCS_SRC ?= ../astrolift-docs/docs
DOCS_DST := internal/portabledocs/content

.PHONY: build test fmt lint clean docs manpages vendor-docs vendor-docs-check vendor-charts vendor-charts-check vendor-skills vendor-skills-check

build: vendor-charts-check
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
	cp "$(DOCS_SRC)/guides/capabilities.md" "$(DOCS_DST)/capabilities.md"
	cp "$(DOCS_SRC)/guides/app-setup.md" "$(DOCS_DST)/app-setup.md"
	cp "$(DOCS_SRC)/guides/agent-setup.md" "$(DOCS_DST)/agent-setup.md"
	cp "$(DOCS_SRC)/guides/workflow-setup.md" "$(DOCS_DST)/workflow-setup.md"
	cp "$(DOCS_SRC)/guides/bounded-workflows.md" "$(DOCS_DST)/bounded-workflows.md"
	cp "$(DOCS_SRC)/guides/shared-services.md" "$(DOCS_DST)/shared-services.md"
	cp "$(DOCS_SRC)/guides/model-hosting.md" "$(DOCS_DST)/model-hosting.md"
	cp "$(DOCS_SRC)/guides/agent-completion-callbacks.md" "$(DOCS_DST)/callbacks.md"
	cp "$(DOCS_SRC)/guides/environment-actions.md" "$(DOCS_DST)/environment-actions.md"
	cp "$(DOCS_SRC)/guides/reviewed-starts.md" "$(DOCS_DST)/reviewed-starts.md"
	cp "$(DOCS_SRC)/guides/preview-targets.md" "$(DOCS_DST)/preview-targets.md"
	cp "$(DOCS_SRC)/guides/cluster-log-collector.md" "$(DOCS_DST)/cluster-log-collector.md"
	cp "$(DOCS_SRC)/guides/cluster-agent-install.md" "$(DOCS_DST)/cluster-agent-install.md"
	cp "$(DOCS_SRC)/guides/logs-traces.md" "$(DOCS_DST)/logs-traces.md"
	cp "$(DOCS_SRC)/guides/workload-signals.md" "$(DOCS_DST)/workload-signals.md"
	cp "$(DOCS_SRC)/llms.txt" "$(DOCS_DST)/llms.txt"

vendor-docs-check:
	@for file in start.md client.md cli.md api.md mcp.md manifest.md agents.md workflows.md capabilities.md app-setup.md agent-setup.md workflow-setup.md bounded-workflows.md shared-services.md model-hosting.md callbacks.md environment-actions.md reviewed-starts.md preview-targets.md cluster-agent-install.md cluster-log-collector.md logs-traces.md workload-signals.md llms.txt; do \
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
			"guides/capabilities.md:capabilities.md" \
			"guides/app-setup.md:app-setup.md" \
			"guides/agent-setup.md:agent-setup.md" \
			"guides/workflow-setup.md:workflow-setup.md" \
			"guides/bounded-workflows.md:bounded-workflows.md" \
			"guides/shared-services.md:shared-services.md" \
			"guides/model-hosting.md:model-hosting.md" \
			"guides/agent-completion-callbacks.md:callbacks.md" \
			"guides/environment-actions.md:environment-actions.md" \
			"guides/reviewed-starts.md:reviewed-starts.md" \
			"guides/preview-targets.md:preview-targets.md" \
			"guides/cluster-log-collector.md:cluster-log-collector.md" \
			"guides/cluster-agent-install.md:cluster-agent-install.md" \
			"guides/logs-traces.md:logs-traces.md" \
			"guides/workload-signals.md:workload-signals.md" \
			"llms.txt:llms.txt"; do \
			source="$${pair%%:*}"; destination="$${pair#*:}"; \
			cmp -s "$(DOCS_SRC)/$$source" "$(DOCS_DST)/$$destination" || { \
				echo "stale $(DOCS_DST)/$$destination — run \`make vendor-docs\`"; \
				exit 1; \
			}; \
		done; \
	fi

# Refresh from the exact published Git tree, then build Chart.lock dependencies.
# Refuses dirty, older or unpublished source checkouts before any overwrite.
vendor-charts:
	@test -n "$(PREREQS_REV)" || { echo "set PREREQS_REV to the full published opscode commit"; exit 1; }
	python3 scripts/vendor-charts.py refresh --source "$(PREREQS_REPO)" --revision "$(PREREQS_REV)"

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

# Offline CI proves complete file-set/content integrity of the pinned snapshot.
# Canonical remote parity is reviewed separately before publishing this pin.
vendor-charts-check:
	python3 scripts/vendor-charts.py check
