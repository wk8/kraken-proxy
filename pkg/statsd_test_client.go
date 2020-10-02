package pkg

import (
	"time"

	"github.com/cactus/go-statsd-client/statsd"
)

// testStatsdClient is a simple in-memory statsd.StatSender implementation, for test purposes.
type testStatsdClient struct {
	calls []statsdCall
}

type statsdCall struct {
	methodName string
	stat       string

	// exactly one of valueInt and valueStr is relevant, depending on methodName.
	valueInt int64
	valueStr string

	rate float32
}

var _ statsd.StatSender = &testStatsdClient{}

func (c *testStatsdClient) Inc(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statsdCall{
		methodName: "Inc",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) Dec(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statsdCall{
		methodName: "Dec",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) Gauge(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statsdCall{
		methodName: "Gauge",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) GaugeDelta(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statsdCall{
		methodName: "GaugeDelta",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) Timing(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statsdCall{
		methodName: "Timing",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) TimingDuration(stat string, duration time.Duration, rate float32) error {
	c.calls = append(c.calls, statsdCall{
		methodName: "TimingDuration",
		stat:       stat,
		valueInt:   int64(duration),
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) Set(stat string, value string, rate float32) error {
	c.calls = append(c.calls, statsdCall{
		methodName: "Set",
		stat:       stat,
		valueStr:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) SetInt(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statsdCall{
		methodName: "SetInt",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) Raw(stat string, value string, rate float32) error {
	c.calls = append(c.calls, statsdCall{
		methodName: "Raw",
		stat:       stat,
		valueStr:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) reset() []statsdCall {
	calls := c.calls
	c.calls = nil
	return calls
}
