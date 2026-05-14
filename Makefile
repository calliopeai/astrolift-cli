BINARY   := astro
MODULE   := github.com/calliopeai/astrolift-cli
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -ldflags "-X $(MODULE)/cmd.Version=$(VERSION)"

# Vendored chart source — refreshed by `make vendor-charts` before build.
# The metarepo path is used in dev; CI checks the committed copy and skips
# vendor-charts via SKIP_VENDOR_CHARTS=1 (the chart lives in-repo so the
# binary can be built without the metarepo sibling on disk).
PREREQS_SRC ?= ../astrolift-opscode/helm/astrolift-prereqs
PREREQS_DST := internal/charts/astrolift-prereqs

.PHONY: build test fmt lint clean vendor-charts vendor-charts-check

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
