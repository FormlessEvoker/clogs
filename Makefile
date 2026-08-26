APP := clogs
BIN_DIR := bin
CMD := ./cmd/clogs
VERSION ?= dev
GO ?= go
GOFMT ?= gofmt
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.git/*')
LDFLAGS := -X github.com/FormlessEvoker/clogs/internal/cli.BuildVersion=$(VERSION)

.PHONY: build cross-build release-build test test-race vet lint fmt fmt-check content-check check clean

build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(APP) $(CMD)

# Compile every published release target without packaging. CI runs this on
# pull requests so a target that stops building fails there, not mid-release.
cross-build:
	ARCHIVE=0 VERSION=$(VERSION) ./scripts/build-release.sh

# Build and package every release target into $(BIN_DIR)/dist, with checksums.
release-build:
	VERSION=$(VERSION) ./scripts/build-release.sh

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
