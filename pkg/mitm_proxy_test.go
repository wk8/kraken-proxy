package pkg

import (
	"bufio"
	"context"
	"fmt"
	"github.com/cactus/go-statsd-client/statsd"
	"github.com/stretchr/testify/assert"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type dummyUpstreamServer struct {
	t             *testing.T
	visitedRoutes []string
}

// the various strings used in responses
var (
	ok          = []byte("ok\n")
	helloWorld  = []byte("hello brave new world!\n")
	slow        = []byte("slow\n")
	directReply = []byte("bim bam\n")
	// this one needs to be big enough to not be buffered anywhere for too long
	streamData           = []byte(strings.Repeat("data", 100000) + "\n")
	fallbackFailedHijack = []byte("sorry jack, can't work every time\n")
)

func (s *dummyUpstreamServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.visitedRoutes = append(s.visitedRoutes, request.URL.Path)

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
	case "/redirect":
		// just replies with 301
		writer.Header().Add("Location", "http://who.cares")
		writer.WriteHeader(http.StatusMovedPermanently)
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
	routes := s.visitedRoutes
	s.visitedRoutes = nil
	return routes
}

func TestMitmProxy(t *testing.T) {
	// first, let's start the upstream server, the one we're going to be proxying
	upstreamServer := &dummyUpstreamServer{
		t: t,
	}
	upstreamPort, upstreamCleanup := withDummyUpstreamServer(t, upstreamServer)
	defer upstreamCleanup()

	// sanity check: we should be able to talk to the upstream directly
	baseUrl := "https://" + localhostAddr(upstreamPort)
	upstreamClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:       tlsClientConfig(t),
			ResponseHeaderTimeout: 1 * time.Second,
		},
		// don't follow redirects...
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, respBody := makeRequest(t, upstreamClient, baseUrl, "/ok")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, ok, respBody)

	// now let's start the proxy
	hijacker := &MitmHijacker{
		requestHandler: func(writer http.ResponseWriter, request *http.Request) (bool, *http.Request, *http.Client) {
			switch request.URL.Path {
			case "/hijack_me":
				newRequest, err := http.NewRequest(request.Method, baseUrl+"/hello_world", request.Body)
				require.NoError(t, err)

				return false, newRequest, upstreamClient
			case "/hijack_to_stream":
				newRequest, err := http.NewRequest(request.Method, baseUrl+"/stream", request.Body)
				require.NoError(t, err)

				return false, newRequest, upstreamClient
			case "/hijack_to_slow":
				newRequest, err := http.NewRequest(request.Method, baseUrl+"/slow", request.Body)
				require.NoError(t, err)

				return false, newRequest, upstreamClient
			case "/hijack_to_redirect":
				newRequest, err := http.NewRequest(request.Method, baseUrl+"/redirect", request.Body)
				require.NoError(t, err)

				return false, newRequest, upstreamClient
			case "/direct_reply":
				writer.Header().Add("coucou", "toi")
				writer.WriteHeader(http.StatusAccepted)
				_, err := writer.Write(directReply)
				require.NoError(t, err)

				return true, nil, nil
			default:
				return false, nil, nil
			}
		},
	}
	statdsClient := &testStatsdClient{}
	proxyPort, proxyCleanup := withTestProxy(t, hijacker, statdsClient)
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

	t.Run("with a simple proxy-ed route", func(t *testing.T) {
		upstreamServer.reset()
		statdsClient.reset()

		resp, respBody := makeRequest(t, proxyClient, baseUrl, "/ok")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, ok, respBody)

		assert.Equal(t, []string{"/ok"}, upstreamServer.reset())
		assert.Equal(t, []statdsCall{{methodName: "Inc", stat: "mitm.proxyed.count", valueInt: 1, valueStr: "", rate: 1}}, statdsClient.reset())
	})

	t.Run("with a simple proxy-ed route with headers", func(t *testing.T) {
		upstreamServer.reset()
		statdsClient.reset()

		resp, respBody := makeRequest(t, proxyClient, baseUrl, "/hello_world")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, helloWorld, respBody)
		assert.Equal(t, "new_world", resp.Header.Get("Brave"))

		assert.Equal(t, []string{"/hello_world"}, upstreamServer.reset())
		assert.Equal(t, []statdsCall{{methodName: "Inc", stat: "mitm.proxyed.count", valueInt: 1, valueStr: "", rate: 1}}, statdsClient.reset())
	})

	t.Run("with a route hijacked to somewhere else", func(t *testing.T) {
		upstreamServer.reset()
		statdsClient.reset()

		resp, respBody := makeRequest(t, proxyClient, baseUrl, "/hijack_me")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, helloWorld, respBody)
		// headers should have been passed along too
		assert.Equal(t, "new_world", resp.Header.Get("Brave"))

		assert.Equal(t, []string{"/hello_world"}, upstreamServer.reset())
		assert.Equal(t, []statdsCall{{methodName: "Inc", stat: "mitm.hijacked.request.success.count", valueInt: 1, valueStr: "", rate: 1}}, statdsClient.reset())
	})

	t.Run("with a route hijacked to a direct reply", func(t *testing.T) {
		upstreamServer.reset()
		statdsClient.reset()

		resp, respBody := makeRequest(t, proxyClient, baseUrl, "/direct_reply")

		assert.Equal(t, http.StatusAccepted, resp.StatusCode)
		assert.Equal(t, directReply, respBody)
		assert.Equal(t, "toi", resp.Header.Get("coucou"))

		assert.Equal(t, 0, len(upstreamServer.reset()))
		assert.Equal(t, []statdsCall{{methodName: "Inc", stat: "mitm.hijacked.direct_reply.count", valueInt: 1, valueStr: "", rate: 1}}, statdsClient.reset())
	})

	for _, testCase := range []struct {
		routeType         string
		route             string
		counterMetricName MitmProxyStatsdMetricName
		paceMetricName    MitmProxyStatsdMetricName
	}{
		{
			routeType:         "proxy-ed",
			route:             "/stream",
			counterMetricName: MitMProxyedRequestCounter,
			paceMetricName:    MitMProxyedRequestTransferPace,
		},
		{
			routeType:         "hijacked",
			route:             "/hijack_to_stream",
			counterMetricName: MitMSuccessfullyHijackedToRequestCounter,
			paceMetricName:    MitMHijackedRequestTransferPace,
		},
	} {
		t.Run(fmt.Sprintf("with a %s route that slowly streams data, the data is passed along to the client at the same rate", testCase.routeType), func(t *testing.T) {
			upstreamServer.reset()
			statdsClient.reset()

			startedAt := time.Now()
			response, err := proxyClient.Get(baseUrl + testCase.route)
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
			metrics := statdsClient.reset()
			if assert.Equal(t, 2, len(metrics)) {
				assert.Equal(t, statdsCall{
					methodName: "Inc",
					stat:       string(testCase.counterMetricName),
					valueInt:   1,
					rate:       1,
				}, metrics[0])

				paceMetric := metrics[1]
				assert.Equal(t, "TimingDuration", paceMetric.methodName)
				assert.Equal(t, string(testCase.paceMetricName), paceMetric.stat)
				assert.Equal(t, float32(1), paceMetric.rate)

				// we should have transmitted 7 times the length of streamData over the course of about 3 seconds
				expectedPace := 3 * time.Second / time.Duration(7*len(streamData)/1000)
				pace := time.Duration(paceMetric.valueInt)
				assert.True(t, pace >= expectedPace)
				assert.True(t, pace <= 3*expectedPace/2)
			}
		})
	}

	t.Run("if a hijacked request succeeds, but is not deemed acceptable, it falls back to upstream", func(t *testing.T) {
		upstreamServer.reset()
		resp, respBody := makeRequest(t, proxyClient, baseUrl, "/hijack_to_redirect")
		// should hit upstream, which doesn't know that route
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, 0, len(respBody))
		assert.Equal(t, []string{"/redirect", "/hijack_to_redirect"}, upstreamServer.reset())
	})

	t.Run("if a hijacked request errors out (eg times out), it falls back to upstream", func(t *testing.T) {
		upstreamServer.reset()

		startedAt := time.Now()
		resp, respBody := makeRequest(t, proxyClient, baseUrl, "/hijack_to_slow")
		elapsed := time.Since(startedAt)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, fallbackFailedHijack, respBody)

		// the timeout for first byte is set to one second on the upstream client
		t.Logf("Elasped: %v", elapsed)
		assert.True(t, elapsed > 1*time.Second)
		assert.True(t, elapsed < 3*time.Second/2)

		assert.Equal(t, []string{"/slow", "/hijack_to_slow"}, upstreamServer.reset())
	})
}

/*** Helpers below ***/

const genericTimeout = 5 * time.Second

// sets up a test MitmProxy, and returns its port as well as a function to tear it down when done testing
func withTestProxy(t *testing.T, hijacker *MitmHijacker, statsdClient statsd.StatSender) (int, func()) {
	ca, caCleanup := withTestCAFiles(t)

	port := getAvailablePort(t)
	proxy := NewMitmProxy(localhostAddr(port), ca, hijacker, statsdClient)

	listeningChan := make(chan interface{})

	go func() {
		require.NoError(t, proxy.start(listeningChan, tlsClientConfig(t)))
	}()

	select {
	case <-listeningChan:
	case <-time.After(genericTimeout):
		t.Fatalf("Timed out waiting for test mitm server to start listening on %d", port)
	}

	return port, func() {
		caCleanup()
		require.NoError(t, proxy.Stop())
	}
}

// sets up a dummy server, and returns its port as well as a function to tear it down when done testing
func withDummyUpstreamServer(t *testing.T, handler http.Handler) (int, func()) {
	tlsInfo, tlsCleanup := withTestServerTLSFiles(t)

	port := getAvailablePort(t)

	server := &http.Server{
		Addr:    localhostAddr(port),
		Handler: handler,
	}

	listeningChan := make(chan interface{})

	go func() {
		require.NoError(t, startHttpServer(server, listeningChan, tlsInfo, ""))
	}()

	select {
	case <-listeningChan:
	case <-time.After(genericTimeout):
		t.Fatalf("Timed out waiting for dummy upstream server to start listening on %d", port)
	}

	return port, func() {
		tlsCleanup()

		ctx, cancel := context.WithTimeout(context.Background(), genericTimeout)
		defer cancel()
		require.NoError(t, server.Shutdown(ctx))
	}
}

// getAvailablePort asks the kernel for an available port, that is ready to use.
func getAvailablePort(t *testing.T) int {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	require.Nil(t, err)

	listen, err := net.ListenTCP("tcp", addr)
	require.Nil(t, err)

	defer listen.Close()
	return listen.Addr().(*net.TCPAddr).Port
}

func localhostAddr(port int) string {
	return fmt.Sprintf("localhost:%d", port)
}

func makeRequest(t *testing.T, client *http.Client, baseUrl, route string) (response *http.Response, body []byte) {
	response, err := client.Get(baseUrl + route)
	require.NoError(t, err)

	body, err = ioutil.ReadAll(response.Body)
	require.NoError(t, response.Body.Close())
	require.NoError(t, err)

	return
}
