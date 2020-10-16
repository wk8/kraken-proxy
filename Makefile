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

IMAGE_NAME = wk88/kraken-proxy

.PHONY: image
image:
	docker build . -t $(IMAGE_NAME)

.PHONY: push
push:
	docker push $(IMAGE_NAME)

.PHONY: upload
upload:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o kraken-proxy-linux-amd64 -ldflags="-w -s" github.com/wk8/kraken-proxy/cmd/kraken-proxy
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o kraken-proxy-linux-arm64 -ldflags="-w -s" github.com/wk8/kraken-proxy/cmd/kraken-proxy
	aws s3 cp kraken-proxy-linux-amd64 s3://kraken-proxy/kraken-proxy-linux-amd64
	aws s3 cp kraken-proxy-linux-arm64 s3://kraken-proxy/kraken-proxy-linux-arm64
