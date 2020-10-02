.DEFAULT_GOAL := all

.PHONY: all
all: test lint build

# the TEST_FLAGS env var can be set to eg run only specific tests
.PHONY: test
test:
	go test ./... -v -count=1 -race -cover "$$TEST_FLAGS"

.PHONY: lint
lint:
	golangci-lint run

.PHONY: build
build:
	go build -o kraken-proxy github.com/wk8/kraken-proxy/cmd/kraken-proxy
