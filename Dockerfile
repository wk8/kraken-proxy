ARG GO_VERSION
FROM golang:1.15 AS builder

WORKDIR /go/src/github.com/wk8/kraken-proxy

COPY go.* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o kraken-proxy -ldflags="-w -s" github.com/wk8/kraken-proxy/cmd/kraken-proxy

####

FROM alpine

COPY --from=builder /go/src/github.com/wk8/kraken-proxy/kraken-proxy .

ENTRYPOINT ["./kraken-proxy"]
