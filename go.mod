module github.com/wk8/kraken-proxy

go 1.15

require (
	github.com/cactus/go-statsd-client v3.1.1+incompatible
	github.com/jessevdk/go-flags v1.4.0
	github.com/kr/mitm v0.0.0-00010101000000-000000000000
	github.com/pkg/errors v0.8.0
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.3.0
	github.com/uber/kraken v0.1.4
)

replace github.com/kr/mitm => github.com/wk8/mitm v0.0.0-20180423001252-44941974427c
