.DEFAULT_GOAL := all

SHELL := /bin/bash

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

GITHUB_USER ?= wk8
GITHUB_REPO ?= kraken-proxy

.PHONY: release
release: _github_release
	git push && [ -z "$$(git status --porcelain)" ]

	if [ ! "$$TAG" ]; then echo 'TAG env var not set' && exit 1; fi

	VERSION="$$TAG-$$(git rev-parse HEAD)" && LDFLAGS="-w -s -X github.com/wk8/kraken-proxy/version.VERSION=$$VERSION" \
		&& GOOS=darwin GOARCH=amd64 go build -o kraken-proxy-osx-amd64 -ldflags="$$LDFLAGS" github.com/wk8/kraken-proxy/cmd/kraken-proxy \
		&& GOOS=linux GOARCH=amd64 go build -o kraken-proxy-linux-amd64 -ldflags="$$LDFLAGS" github.com/wk8/kraken-proxy/cmd/kraken-proxy \
	  	&& GOOS=linux GOARCH=arm64 go build -o kraken-proxy-linux-arm64 -ldflags="$$LDFLAGS" github.com/wk8/kraken-proxy/cmd/kraken-proxy

	git tag "$$TAG" && git push --tags

	github-release release --user $(GITHUB_USER) --repo $(GITHUB_REPO) --tag "$$TAG"
	github-release upload --user $(GITHUB_USER) --repo $(GITHUB_REPO) --tag "$$TAG" --file kraken-proxy-osx-amd64 --name kraken-proxy-osx-amd64
	github-release upload --user $(GITHUB_USER) --repo $(GITHUB_REPO) --tag "$$TAG" --file kraken-proxy-linux-amd64 --name kraken-proxy-linux-amd64
	github-release upload --user $(GITHUB_USER) --repo $(GITHUB_REPO) --tag "$$TAG" --file kraken-proxy-linux-arm64 --name kraken-proxy-linux-arm64

# see https://github.com/github-release/github-release
.PHONY: _github_release
_github_release:
	which github-release &> /dev/null || go get -u github.com/github-release/github-release
	if [ ! "$$GITHUB_TOKEN" ]; then echo 'GITHUB_TOKEN env var not set' && exit 1; fi
