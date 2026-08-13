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

# Canonical public documentation lives in astrolift-docs. A deliberately
# small release-matched snapshot is committed here so `astro docs` and the
# generated man pages work without a network connection or sibling checkout.
DOCS_SRC ?= ../astrolift-docs/docs
DOCS_DST := internal/portabledocs/content

.PHONY: build test fmt lint clean docs manpages vendor-docs vendor-docs-check vendor-charts vendor-charts-check

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
	cp "$(DOCS_SRC)/guides/clients.md" "$(DOCS_DST)/client.md"
	cp "$(DOCS_SRC)/reference/cli.md" "$(DOCS_DST)/cli.md"
	cp "$(DOCS_SRC)/reference/api.md" "$(DOCS_DST)/api.md"
	cp "$(DOCS_SRC)/reference/mcp.md" "$(DOCS_DST)/mcp.md"
	cp "$(DOCS_SRC)/reference/astrolift-toml.md" "$(DOCS_DST)/manifest.md"
	cp "$(DOCS_SRC)/reference/agent-packages.md" "$(DOCS_DST)/agents.md"
	cp "$(DOCS_SRC)/reference/workflow-toml.md" "$(DOCS_DST)/workflows.md"
	cp "$(DOCS_SRC)/llms.txt" "$(DOCS_DST)/llms.txt"

vendor-docs-check:
	@for file in client.md cli.md api.md mcp.md manifest.md agents.md workflows.md llms.txt; do \
		test -s "$(DOCS_DST)/$$file" || { echo "missing $(DOCS_DST)/$$file — run \`make vendor-docs\`"; exit 1; }; \
	done

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

# CI sanity check — the committed copy must exist and contain Chart.yaml.
vendor-charts-check:
	@test -f $(PREREQS_DST)/Chart.yaml || (echo "missing $(PREREQS_DST)/Chart.yaml — run \`make vendor-charts\`" && exit 1)
