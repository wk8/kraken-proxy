package pkg

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cactus/go-statsd-client/statsd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummyUpstreamServer struct {
	t             *testing.T
	visitedRoutes []string
	mutex         sync.Mutex
}

// the various strings used in responses.
var (
	ok          = []byte("ok\n")
	helloWorld  = []byte("hello brave new world!\n")
	directReply = []byte("bim bam\n")
	// this one needs to be big enough to not be buffered anywhere for too long.
	streamData           = []byte(strings.Repeat("data", 100000) + "\n")
	fallbackFailedHijack = []byte("sorry jack, can't work every time\n")
)

func (s *dummyUpstreamServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.mutex.Lock()
	s.visitedRoutes = append(s.visitedRoutes, request.URL.Path)
	s.mutex.Unlock()

	switch request.URL.Path {
	case "/ok":
		writer.WriteHeader(http.StatusOK)
		_, err := writer.Write(ok)
		require.NoError(s.t, err)
	case "/hello_world":
		writer.Header().Add("brave", "new_world")
		writer.WriteHeader(http.StatusOK)
		_, err := writer.Write(helloWorld)
		require.NoError(s.t, err)
	case "/slow":
		// sleeps 3 seconds before sending anything
		time.Sleep(3 * time.Second)
		writer.WriteHeader(http.StatusOK)
	case "/hijack_to_slow":
		writer.WriteHeader(http.StatusOK)
		_, err := writer.Write(fallbackFailedHijack)
		require.NoError(s.t, err)
	case "/stream":
		// immediately replies with 200, and then starts writing "ok" every 0.5 seconds, for 3 seconds, flushing every time
		writer.WriteHeader(http.StatusOK)

		flusher := writer.(http.Flusher)
		for i := 0; i < 7; i++ {
			_, err := writer.Write(streamData)
			require.NoError(s.t, err)
			flusher.Flush()

			time.Sleep(time.Second / 2)
		}
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (s *dummyUpstreamServer) reset() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	routes := s.visitedRoutes
	s.visitedRoutes = nil
	return routes
}

type testMitmHijacker struct {
	*DefaultMitmHijacker

	t              *testing.T
	upstreamClient *http.Client
	baseURL        string
}

var _ MitmHijacker = &testMitmHijacker{}

func (h *testMitmHijacker) RequestHandler(writer http.ResponseWriter, request *http.Request) (hijacked bool, response *http.Response, err error) {
	var newRequest *http.Request

	switch request.URL.Path {
	case "/hijack_me":
		newRequest, err = http.NewRequest(request.Method, h.baseURL+"/hello_world", request.Body)
	case "/hijack_to_stream":
		newRequest, err = http.NewRequest(request.Method, h.baseURL+"/stream", request.Body)
	case "/hijack_to_slow":
		newRequest, err = http.NewRequest(request.Method, h.baseURL+"/slow", request.Body)
	case "/direct_reply":
		writer.Header().Add("coucou", "toi")
		writer.WriteHeader(http.StatusAccepted)
		_, err = writer.Write(directReply)
	case "/ok_transform_metric":
		newRequest, err = http.NewRequest(request.Method, h.baseURL+"/ok", request.Body)
	default:
		return false, nil, nil
	}

	require.NoError(h.t, err)
	if newRequest != nil {
		response, err = h.upstreamClient.Do(newRequest)
	}
	return true, response, err
}

func (h *testMitmHijacker) TransformMetricName(name MitmProxyStatsdMetricName, request *http.Request) string {
	switch request.URL.Path {
	case "/ok_transform_metric":
		return "transformed_metric"
	default:
		return h.DefaultMitmHijacker.TransformMetricName(name, request)
	}
}

func TestMitmProxy(t *testing.T) {
	// first, let's start the upstream server, the one we're going to be proxying
	upstreamServer := &dummyUpstreamServer{
		t: t,
	}
	upstreamPort, upstreamCleanup := withDummyUpstreamServer(t, upstreamServer)
	defer upstreamCleanup()

	// sanity check: we should be able to talk to the upstream directly
	baseURL := "https://" + localhostAddr(upstreamPort)
	upstreamClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:       tlsClientConfig(t),
			ResponseHeaderTimeout: 1 * time.Second,
		},
	}
	resp, respBody := makeRequest(t, upstreamClient, baseURL, "/ok")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, ok, respBody)

	// now let's start the proxy
	hijacker := &testMitmHijacker{
		DefaultMitmHijacker: &DefaultMitmHijacker{},
		t:                   t,
		upstreamClient:      upstreamClient,
		baseURL:             baseURL,
	}
	statsdClient := &testStatsdClient{}
	proxyPort, proxyCleanup := withTestProxy(t, hijacker, statsdClient)
	defer proxyCleanup()

	// and let's create a HTTP client that goes through it
	proxyURL, err := url.Parse("http://" + localhostAddr(proxyPort))
	require.NoError(t, err)
	proxyClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsClientConfig(t),
			Proxy:           http.ProxyURL(proxyURL),
		},
	}

	t.Run("with a simple proxied route", func(t *testing.T) {
		upstreamServer.reset()
		statsdClient.reset()

		resp, respBody := makeRequest(t, proxyClient, baseURL, "/ok")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, ok, respBody)

		assert.Equal(t, []string{"/ok"}, upstreamServer.reset())
		assert.Equal(t, []statsdCall{{methodName: "Inc", stat: string(ProxiedRequestCounter), valueInt: 1, valueStr: "", rate: 1}}, statsdClient.reset())
	})

	t.Run("with a simple proxied route with headers", func(t *testing.T) {
		upstreamServer.reset()
		statsdClient.reset()

		resp, respBody := makeRequest(t, proxyClient, baseURL, "/hello_world")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, helloWorld, respBody)
		assert.Equal(t, "new_world", resp.Header.Get("Brave"))

		assert.Equal(t, []string{"/hello_world"}, upstreamServer.reset())
		assert.Equal(t, []statsdCall{{methodName: "Inc", stat: string(ProxiedRequestCounter), valueInt: 1, valueStr: "", rate: 1}}, statsdClient.reset())
	})

	t.Run("with a route hijacked to somewhere else", func(t *testing.T) {
		upstreamServer.reset()
		statsdClient.reset()

		resp, respBody := makeRequest(t, proxyClient, baseURL, "/hijack_me")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, helloWorld, respBody)
		// headers should have been passed along too
		assert.Equal(t, "new_world", resp.Header.Get("Brave"))

		assert.Equal(t, []string{"/hello_world"}, upstreamServer.reset())
		assert.Equal(t, []statsdCall{{methodName: "Inc", stat: string(HijackedRequestCounter), valueInt: 1, valueStr: "", rate: 1}}, statsdClient.reset())
	})

	t.Run("with a route hijacked to a direct reply", func(t *testing.T) {
		upstreamServer.reset()
		statsdClient.reset()

		resp, respBody := makeRequest(t, proxyClient, baseURL, "/direct_reply")

		assert.Equal(t, http.StatusAccepted, resp.StatusCode)
		assert.Equal(t, directReply, respBody)
		assert.Equal(t, "toi", resp.Header.Get("coucou"))

		assert.Equal(t, 0, len(upstreamServer.reset()))
		assert.Equal(t, []statsdCall{{methodName: "Inc", stat: string(HijackedRequestCounter), valueInt: 1, valueStr: "", rate: 1}}, statsdClient.reset())
	})

	for _, testCase := range []struct {
		routeType         string
		route             string
		counterMetricName string
		paceMetricName    string
	}{
		{
			routeType:         "proxied",
			route:             "/stream",
			counterMetricName: string(ProxiedRequestCounter),
			paceMetricName:    string(ProxiedRequestTransferPace),
		},
		{
			routeType:         "hijacked",
			route:             "/hijack_to_stream",
			counterMetricName: string(HijackedRequestCounter),
			paceMetricName:    string(HijackedRequestTransferPace),
		},
	} {
		t.Run(fmt.Sprintf("with a %s route that slowly streams data, the data is passed along to the client at the same rate", testCase.routeType), func(t *testing.T) {
			upstreamServer.reset()
			statsdClient.reset()

			startedAt := time.Now()
			response, err := proxyClient.Get(baseURL + testCase.route)
			require.NoError(t, err)
			// should receive the 200 header before we start streaming the actual data
			timeToFirstByte := time.Since(startedAt)
			t.Logf("Time to 1st byte: %v", timeToFirstByte)
			assert.True(t, timeToFirstByte < time.Second/4)
			assert.Equal(t, http.StatusOK, response.StatusCode)

			defer response.Body.Close()
			lines := 0
			tick := startedAt
			reader := bufio.NewReader(response.Body)
			for {
				line, err := reader.ReadBytes('\n')

				if err != nil {
					if err == io.EOF {
						break
					}
					assert.NoError(t, err)
				}

				lines++
				assert.Equal(t, streamData, line)

				elapsed := time.Since(tick)
				t.Logf("Time elapsed since last line: %v", elapsed)
				assert.True(t, elapsed > time.Second/3)
				assert.True(t, elapsed < time.Second)

				tick = time.Now()
			}
			assert.Equal(t, 7, lines)

			assert.Equal(t, []string{"/stream"}, upstreamServer.reset())
			metrics := statsdClient.reset()
			if assert.Equal(t, 2, len(metrics)) {
				assert.Equal(t, statsdCall{
					methodName: "Inc",
					stat:       testCase.counterMetricName,
					valueInt:   1,
					rate:       1,
				}, metrics[0])

				paceMetric := metrics[1]
				assert.Equal(t, "TimingDuration", paceMetric.methodName)
				assert.Equal(t, testCase.paceMetricName, paceMetric.stat)
				assert.Equal(t, float32(1), paceMetric.rate)

				// we should have transmitted 7 times the length of streamData over the course of about 3 seconds
				expectedPace := 3 * time.Second / time.Duration(7*len(streamData)/1000)
				pace := time.Duration(paceMetric.valueInt)
				assert.True(t, pace >= expectedPace)
				assert.True(t, pace <= 3*expectedPace/2)
			}
		})
	}

	t.Run("if a hijacked request errors out (eg times out), it falls back to upstream", func(t *testing.T) {
		upstreamServer.reset()
		statsdClient.reset()

		startedAt := time.Now()
		resp, respBody := makeRequest(t, proxyClient, baseURL, "/hijack_to_slow")
		elapsed := time.Since(startedAt)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, fallbackFailedHijack, respBody)

		// the timeout for first byte is set to one second on the upstream client
		t.Logf("Elapsed: %v", elapsed)
		assert.True(t, elapsed > 1*time.Second)
		assert.True(t, elapsed < 3*time.Second/2)

		assert.Equal(t, []string{"/slow", "/hijack_to_slow"}, upstreamServer.reset())
		assert.Equal(t, []statsdCall{{methodName: "Inc", stat: string(HijackingErrorsCounter), valueInt: 1, valueStr: "", rate: 1},
			{methodName: "Inc", stat: string(ProxiedRequestCounter), valueInt: 1, valueStr: "", rate: 1}}, statsdClient.reset())
	})

	t.Run("hijackers can change metric names", func(t *testing.T) {
		upstreamServer.reset()
		statsdClient.reset()

		resp, respBody := makeRequest(t, proxyClient, baseURL, "/ok_transform_metric")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, ok, respBody)

		assert.Equal(t, []string{"/ok"}, upstreamServer.reset())
		assert.Equal(t, []statsdCall{{methodName: "Inc", stat: "transformed_metric", valueInt: 1, valueStr: "", rate: 1}}, statsdClient.reset())
	})
}

/*** Helpers below ***/

// sets up a test MitmProxy, and returns its port as well as a function to tear it down when done testing.
func withTestProxy(t *testing.T, hijacker MitmHijacker, statsdClient statsd.StatSender) (int, func()) {
	ca, caCleanup := withTestCAFiles(t)

	port := getAvailablePort(t)
	proxy := NewMitmProxy(localhostAddr(port), ca, hijacker, statsdClient)

	listeningChan := make(chan interface{})

	go func() {
		require.NoError(t, proxy.start(listeningChan, tlsClientConfig(t)))
	}()

	select {
	case <-listeningChan:
	case <-time.After(genericTestTimeout):
		t.Fatalf("Timed out waiting for test mitm server to start listening on %d", port)
	}

	return port, func() {
		caCleanup()
		require.NoError(t, proxy.Stop())
	}
}

// sets up a dummy server, and returns its port as well as a function to tear it down when done testing.
func withDummyUpstreamServer(t *testing.T, handler http.Handler) (int, func()) {
	tlsInfo, tlsCleanup := withTestServerTLSFiles(t)

	port := getAvailablePort(t)

	server := &http.Server{
		Addr:    localhostAddr(port),
		Handler: handler,
	}

	listeningChan := make(chan interface{})

	go func() {
		require.NoError(t, startHTTPServer(server, listeningChan, tlsInfo, ""))
	}()

	select {
	case <-listeningChan:
	case <-time.After(genericTestTimeout):
		t.Fatalf("Timed out waiting for dummy upstream server to start listening on %d", port)
	}

	return port, func() {
		tlsCleanup()

		ctx, cancel := context.WithTimeout(context.Background(), genericTestTimeout)
		defer cancel()
		require.NoError(t, server.Shutdown(ctx))
	}
}
