BINARY   := astro
MODULE   := github.com/calliopeai/astrolift-cli
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -ldflags "-X $(MODULE)/cmd.Version=$(VERSION)"

.PHONY: build test fmt lint clean

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
