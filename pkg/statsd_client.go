package pkg

import (
	"github.com/cactus/go-statsd-client/statsd"
	"time"
)

const (
	defaultFlushInterval = 100 * time.Millisecond
	defaultFlushBytes    = 512
)

func NewStatsdClient(config *Config) (statsd.StatSender, error) {
	if config == nil || config.Statsd == nil || config.Statsd.Address == "" {
		return nil, nil
	}

	flushInterval := config.Statsd.FlushInterval
	if flushInterval == 0 {
		flushInterval = defaultFlushInterval
	}
	flushBytes := config.Statsd.FlushBytes
	if flushBytes == 0 {
		flushBytes = defaultFlushBytes
	}

	return statsd.NewBufferedClient(config.Statsd.Address, config.Statsd.Prefix, flushInterval, flushBytes)
}
