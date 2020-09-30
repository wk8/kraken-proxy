package pkg

import (
	"github.com/cactus/go-statsd-client/statsd"
	"time"
)

// testStatsdClient is a simple in-memory statsd.StatSender implementation, for test purposes
type testStatsdClient struct {
	calls []statdsCall
}

type statdsCall struct {
	methodName string
	stat       string

	// exactly one of valueInt and valueStr is relevant, depending on methodName
	valueInt int64
	valueStr string

	rate float32
}

var _ statsd.StatSender = &testStatsdClient{}

func (c *testStatsdClient) Inc(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statdsCall{
		methodName: "Inc",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) Dec(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statdsCall{
		methodName: "Dec",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) Gauge(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statdsCall{
		methodName: "Gauge",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) GaugeDelta(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statdsCall{
		methodName: "GaugeDelta",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) Timing(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statdsCall{
		methodName: "Timing",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) TimingDuration(stat string, duration time.Duration, rate float32) error {
	c.calls = append(c.calls, statdsCall{
		methodName: "TimingDuration",
		stat:       stat,
		valueInt:   int64(duration),
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) Set(stat string, value string, rate float32) error {
	c.calls = append(c.calls, statdsCall{
		methodName: "Set",
		stat:       stat,
		valueStr:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) SetInt(stat string, value int64, rate float32) error {
	c.calls = append(c.calls, statdsCall{
		methodName: "SetInt",
		stat:       stat,
		valueInt:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) Raw(stat string, value string, rate float32) error {
	c.calls = append(c.calls, statdsCall{
		methodName: "Raw",
		stat:       stat,
		valueStr:   value,
		rate:       rate,
	})
	return nil
}

func (c *testStatsdClient) reset() []statdsCall {
	calls := c.calls
	c.calls = nil
	return calls
}
