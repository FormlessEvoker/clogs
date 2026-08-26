APP := clogs
BIN_DIR := bin
CMD := ./cmd/clogs
VERSION ?= dev
GO ?= go
GOFMT ?= gofmt
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.git/*')
LDFLAGS := -X github.com/FormlessEvoker/clogs/internal/cli.BuildVersion=$(VERSION)

.PHONY: build test test-race vet lint fmt fmt-check content-check check clean

build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(APP) $(CMD)

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint: vet

fmt:
	$(GOFMT) -w $(GO_FILES)

fmt-check:
	@unformatted="$$($(GOFMT) -l $(GO_FILES))"; \
	test -z "$$unformatted" || { echo "Run 'make fmt' to format:"; echo "$$unformatted"; exit 1; }

content-check:
	./scripts/check-prohibited-content.sh

check: fmt-check lint test test-race content-check

clean:
	rm -rf $(BIN_DIR)
