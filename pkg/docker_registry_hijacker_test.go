package pkg

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/pressly/chi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	krakenconfig "github.com/uber/kraken/lib/backend/registrybackend"
	"github.com/uber/kraken/lib/backend/registrybackend/security"
	"github.com/uber/kraken/utils/httputil"
)

func TestDockerRegistryHijackerRequestHandler(t *testing.T) {
	t.Run("it does not hijack requests to unconfigured registries", func(t *testing.T) {
		config := &Config{
			Registries: []Registry{
				{
					Config: krakenconfig.Config{
						Address: "index.docker.io",
					},
					Redirects: []RedirectRegistry{
						{
							Config: krakenconfig.Config{
								Address: "localhost:8765",
							},
						},
					},
				},
			},
		}

		hijacker, err := NewDockerRegistryHijacker(config)
		require.NoError(t, err)

		writer := &dummyResponseWriter{}

		hijacked, response, err := hijacker.RequestHandler(writer, buildGetRequest(t, "https://quay.io/v2/ubuntu/manifests/latest"))

		assert.False(t, hijacked)
		assert.Nil(t, response)
		assert.NoError(t, err)
		assert.False(t, writer.touched)
	})

	t.Run("it does not hijack non-registry requests", func(t *testing.T) {
		config := &Config{
			Registries: []Registry{
				{
					Config: krakenconfig.Config{
						Address: "index.docker.io",
					},
					Redirects: []RedirectRegistry{
						{
							Config: krakenconfig.Config{
								Address: "localhost:8765",
							},
						},
					},
				},
			},
		}

		hijacker, err := NewDockerRegistryHijacker(config)
		require.NoError(t, err)

		writer := &dummyResponseWriter{}

		hijacked, response, err := hijacker.RequestHandler(writer, buildGetRequest(t, "https://index.docker.io/coucou"))

		assert.False(t, hijacked)
		assert.Nil(t, response)
		assert.NoError(t, err)
		assert.False(t, writer.touched)
	})

	t.Run("it handles initial v2 registry auth requests on its own for configured registries", func(t *testing.T) {
		redirectAddress, redirectCleanup := withDummyRegistry(t, 1)
		defer redirectCleanup()

		authRequests, authCleanup := withDummyAuthenticators()
		defer authCleanup()

		config := &Config{
			Registries: []Registry{
				{
					Config: krakenconfig.Config{
						Address: "index.docker.io",
					},
					Redirects: redirects(redirectAddress),
				},
			},
		}

		hijacker, err := NewDockerRegistryHijacker(config)
		require.NoError(t, err)

		writer := &dummyResponseWriter{}

		hijacked, response, err := hijacker.RequestHandler(writer, buildGetRequest(t, "https://index.docker.io/v2/"))

		assert.True(t, hijacked)
		assert.Nil(t, response)
		assert.NoError(t, err)

		if assert.True(t, writer.touched) {
			assert.Equal(t, http.StatusOK, writer.statusCode)
			assert.Equal(t, "{}", string(writer.body))
		}
		assert.Equal(t, 0, len(authRequests.requests))
	})

	t.Run("it successfully redirects to configured registries", func(t *testing.T) {
		redirectAddress, redirectCleanup := withDummyRegistry(t, 1, "ubuntu:18")
		defer redirectCleanup()

		authRequests, authCleanup := withDummyAuthenticators()
		defer authCleanup()

		config := &Config{
			Registries: []Registry{
				{
					Config: krakenconfig.Config{
						Address: "index.docker.io",
					},
					Redirects: redirects(redirectAddress),
				},
			},
		}

		hijacker, err := NewDockerRegistryHijacker(config)
		require.NoError(t, err)

		writer := &dummyResponseWriter{}

		hijacked, response, err := hijacker.RequestHandler(writer, buildGetRequest(t, "https://index.docker.io/v2/ubuntu/blobs/18"))

		assert.True(t, hijacked)
		if assert.NotNil(t, response) {
			assert.Equal(t, http.StatusOK, response.StatusCode)
			assert.Equal(t, "from registry 1: blobs for ubuntu:18", string(readResponseBody(t, response)))
		}
		assert.NoError(t, err)
		assert.False(t, writer.touched)
		if assert.Equal(t, 1, len(authRequests.requests)) {
			assert.Equal(t, redirectAddress, authRequests.requests[0].address)
			assert.Equal(t, "ubuntu", authRequests.requests[0].repo)
		}
	})

	t.Run("it uses configured regexes to match to configured registries", func(t *testing.T) {
		redirectAddress, redirectCleanup := withDummyRegistry(t, 1, "ubuntu:18")
		defer redirectCleanup()

		authRequests, authCleanup := withDummyAuthenticators()
		defer authCleanup()

		config := &Config{
			Registries: []Registry{
				{
					Config: krakenconfig.Config{
						Address: "index.docker.io",
					},
					MatchingRegex: `docker\.io$`,
					Redirects:     redirects(redirectAddress),
				},
			},
		}

		hijacker, err := NewDockerRegistryHijacker(config)
		require.NoError(t, err)

		writer := &dummyResponseWriter{}

		hijacked, response, err := hijacker.RequestHandler(writer, buildGetRequest(t, "https://whatever.docker.io/v2/ubuntu/blobs/18"))

		assert.True(t, hijacked)
		if assert.NotNil(t, response) {
			assert.Equal(t, http.StatusOK, response.StatusCode)
			assert.Equal(t, "from registry 1: blobs for ubuntu:18", string(readResponseBody(t, response)))
		}
		assert.NoError(t, err)
		assert.False(t, writer.touched)
		if assert.Equal(t, 1, len(authRequests.requests)) {
			assert.Equal(t, redirectAddress, authRequests.requests[0].address)
			assert.Equal(t, "ubuntu", authRequests.requests[0].repo)
		}
	})

	t.Run("if several redirect registries are configured, it tries them in order", func(t *testing.T) {
		redirect1Address, redirect1Cleanup := withDummyRegistry(t, 1, "ubuntu:16")
		defer redirect1Cleanup()

		redirect2Address, redirect2Cleanup := withDummyRegistry(t, 2, "ubuntu:18")
		defer redirect2Cleanup()

		authRequests, authCleanup := withDummyAuthenticators()
		defer authCleanup()

		config := &Config{
			Registries: []Registry{
				{
					Config: krakenconfig.Config{
						Address: "index.docker.io",
					},
					Redirects: redirects(redirect1Address, redirect2Address),
				},
			},
		}

		hijacker, err := NewDockerRegistryHijacker(config)
		require.NoError(t, err)

		writer := &dummyResponseWriter{}

		hijacked, response, err := hijacker.RequestHandler(writer, buildGetRequest(t, "https://index.docker.io/v2/ubuntu/blobs/18"))

		assert.True(t, hijacked)
		if assert.NotNil(t, response) {
			assert.Equal(t, http.StatusOK, response.StatusCode)
			assert.Equal(t, "from registry 2: blobs for ubuntu:18", string(readResponseBody(t, response)))
		}
		assert.NoError(t, err)
		assert.False(t, writer.touched)
		if assert.Equal(t, 2, len(authRequests.requests)) {
			assert.Equal(t, redirect1Address, authRequests.requests[0].address)
			assert.Equal(t, "ubuntu", authRequests.requests[0].repo)

			assert.Equal(t, redirect2Address, authRequests.requests[1].address)
			assert.Equal(t, "ubuntu", authRequests.requests[1].repo)
		}
	})

	t.Run("if all redirects fail, it falls back on the original registry, and properly authenticates to it", func(t *testing.T) {
		redirect1Address, redirect1Cleanup := withDummyRegistry(t, 1, "ubuntu:16")
		defer redirect1Cleanup()

		redirect2Address, redirect2Cleanup := withDummyRegistry(t, 2, "ubuntu:14")
		defer redirect2Cleanup()

		redirect3Address, redirect3Cleanup := withDummyRegistry(t, 3, "ubuntu:18")
		defer redirect3Cleanup()

		authRequests, authCleanup := withDummyAuthenticators()
		defer authCleanup()

		config := &Config{
			Registries: []Registry{
				{
					Config: krakenconfig.Config{
						Address: redirect3Address,
						Security: security.Config{
							EnableHTTPFallback: true,
						},
					},
					Redirects: redirects(redirect1Address, redirect2Address),
				},
			},
		}

		hijacker, err := NewDockerRegistryHijacker(config)
		require.NoError(t, err)

		writer := &dummyResponseWriter{}

		hijacked, response, err := hijacker.RequestHandler(writer, buildGetRequest(t, "http://"+redirect3Address+"/v2/ubuntu/manifests/18"))

		assert.True(t, hijacked)
		if assert.NotNil(t, response) {
			assert.Equal(t, http.StatusOK, response.StatusCode)
			assert.Equal(t, "from registry 3: manifests for ubuntu:18", string(readResponseBody(t, response)))
		}
		assert.NoError(t, err)
		assert.False(t, writer.touched)
		if assert.Equal(t, 3, len(authRequests.requests)) {
			assert.Equal(t, redirect1Address, authRequests.requests[0].address)
			assert.Equal(t, "ubuntu", authRequests.requests[0].repo)

			assert.Equal(t, redirect2Address, authRequests.requests[1].address)
			assert.Equal(t, "ubuntu", authRequests.requests[1].repo)

			assert.Equal(t, redirect3Address, authRequests.requests[2].address)
			assert.Equal(t, "ubuntu", authRequests.requests[2].repo)
		}
	})

	t.Run("if configured to re-write repositories, it does it as expected", func(t *testing.T) {
		redirectAddress, redirectCleanup := withDummyRegistry(t, 1, "rewritten_ubuntu$18!:18")
		defer redirectCleanup()

		authRequests, authCleanup := withDummyAuthenticators()
		defer authCleanup()

		config := &Config{
			Registries: []Registry{
				{
					Config: krakenconfig.Config{
						Address: "index.docker.io",
					},
					Redirects: redirects(redirectAddress),
				},
			},
		}
		config.Registries[0].Redirects[0].RewriteRepositories = "rewritten_%r$%t!"

		hijacker, err := NewDockerRegistryHijacker(config)
		require.NoError(t, err)

		writer := &dummyResponseWriter{}

		hijacked, response, err := hijacker.RequestHandler(writer, buildGetRequest(t, "https://index.docker.io/v2/ubuntu/blobs/18"))

		assert.True(t, hijacked)
		if assert.NotNil(t, response) {
			assert.Equal(t, http.StatusOK, response.StatusCode)
			assert.Equal(t, "from registry 1: blobs for rewritten_ubuntu$18!:18", string(readResponseBody(t, response)))
		}
		assert.NoError(t, err)
		assert.False(t, writer.touched)
		if assert.Equal(t, 1, len(authRequests.requests)) {
			assert.Equal(t, redirectAddress, authRequests.requests[0].address)
			assert.Equal(t, "rewritten_ubuntu$18!", authRequests.requests[0].repo)
		}
	})

	t.Run("when hijacking a request, it preserves its headers", func(t *testing.T) {
		redirectAddress, redirectCleanup := withDummyRegistry(t, 1, "ubuntu:18")
		defer redirectCleanup()

		authRequests, authCleanup := withDummyAuthenticators()
		defer authCleanup()

		config := &Config{
			Registries: []Registry{
				{
					Config: krakenconfig.Config{
						Address: "index.docker.io",
					},
					Redirects: redirects(redirectAddress),
				},
			},
		}

		hijacker, err := NewDockerRegistryHijacker(config)
		require.NoError(t, err)

		writer := &dummyResponseWriter{}

		request := buildGetRequest(t, "https://index.docker.io/v2/ubuntu/blobs/18")
		request.Header.Add("double-me", "28")
		hijacked, response, err := hijacker.RequestHandler(writer, request)

		assert.True(t, hijacked)
		if assert.NotNil(t, response) {
			assert.Equal(t, http.StatusOK, response.StatusCode)
			assert.Equal(t, "from registry 1: blobs for ubuntu:18", string(readResponseBody(t, response)))

			assert.Equal(t, "56", response.Header.Get("doubled-ya"))
		}
		assert.NoError(t, err)
		assert.False(t, writer.touched)
		if assert.Equal(t, 1, len(authRequests.requests)) {
			assert.Equal(t, redirectAddress, authRequests.requests[0].address)
			assert.Equal(t, "ubuntu", authRequests.requests[0].repo)
		}
	})
}

/*** Helpers below ***/

// a dummyRegistry gives dummy responses to manifests and blob queries.
type dummyRegistry struct {
	id          int
	knownImages map[string]bool
}

func newDummyRegistry(id int, images ...string) *dummyRegistry {
	knownImages := make(map[string]bool)
	for _, image := range images {
		knownImages[image] = true
	}
	return &dummyRegistry{
		id:          id,
		knownImages: knownImages,
	}
}

func (r *dummyRegistry) start(t *testing.T) (address string, cleanup func()) {
	router := chi.NewRouter()

	router.Get("/v2/{repo}/{queryType}/{tag}", func(writer http.ResponseWriter, request *http.Request) {
		image := fmt.Sprintf("%s:%s", chi.URLParam(request, "repo"), chi.URLParam(request, "tag"))
		if r.knownImages[image] {
			if valueStr := request.Header.Get("double-me"); valueStr != "" {
				value, err := strconv.Atoi(valueStr)
				require.NoError(t, err)

				writer.Header().Add("doubled-ya", strconv.Itoa(value*2))
			}

			writer.WriteHeader(http.StatusOK)

			response := fmt.Sprintf("from registry %d: %s for %s", r.id, chi.URLParam(request, "queryType"), image)
			_, err := writer.Write([]byte(response))
			require.NoError(t, err)
		} else {
			writer.WriteHeader(http.StatusNotFound)
		}
	})

	port := getAvailablePort(t)
	address = localhostAddr(port)

	server := &http.Server{
		Addr:    address,
		Handler: router,
	}

	listeningChan := make(chan interface{})

	go func() {
		require.NoError(t, startHTTPServer(server, listeningChan, nil, ""))
	}()

	select {
	case <-listeningChan:
	case <-time.After(genericTestTimeout):
		t.Fatalf("Timed out waiting for dummy registry server to start listening")
	}

	return address, func() {
		ctx, cancel := context.WithTimeout(context.Background(), genericTestTimeout)
		defer cancel()
		require.NoError(t, server.Shutdown(ctx))
	}
}

func withDummyRegistry(t *testing.T, id int, images ...string) (address string, cleanup func()) {
	registry := newDummyRegistry(id, images...)
	return registry.start(t)
}

type dummyAuthenticator struct {
	address  string
	requests *authRequests
}

var _ security.Authenticator = &dummyAuthenticator{}

type authRequest struct {
	address string
	repo    string
}

type authRequests struct {
	requests []*authRequest
	mutex    sync.Mutex
}

func (d dummyAuthenticator) Authenticate(repo string) ([]httputil.SendOption, error) {
	d.requests.mutex.Lock()
	defer d.requests.mutex.Unlock()

	d.requests.requests = append(d.requests.requests, &authRequest{
		address: d.address,
		repo:    repo,
	})

	return nil, nil
}

// replaces the authenticator factory by one producing dummyAuthenticators, and returns
// both an *authRequests allowing for auth audit, and a func to clean up when done testing.
func withDummyAuthenticators() (*authRequests, func()) {
	previousFactory := authenticatorFactory

	requests := &authRequests{}

	authenticatorFactory = func(config krakenconfig.Config) (security.Authenticator, error) {
		return &dummyAuthenticator{
			address:  config.Address,
			requests: requests,
		}, nil
	}

	return requests, func() {
		authenticatorFactory = previousFactory
	}
}

type noOpReader struct{}

var _ io.Reader = &noOpReader{}

func (*noOpReader) Read(p []byte) (n int, err error) {
	return 0, nil
}

func buildGetRequest(t *testing.T, url string) *http.Request {
	request, err := http.NewRequest("GET", url, &noOpReader{})
	require.NoError(t, err)
	return request
}

type dummyResponseWriter struct {
	statusCode int
	body       []byte
	touched    bool
}

var _ http.ResponseWriter = &dummyResponseWriter{}

func (w *dummyResponseWriter) Header() http.Header {
	w.touched = true
	return nil
}

func (w *dummyResponseWriter) Write(bytes []byte) (int, error) {
	w.touched = true
	w.body = append(w.body, bytes...)
	return len(bytes), nil
}

func (w *dummyResponseWriter) WriteHeader(statusCode int) {
	w.touched = true
	w.statusCode = statusCode
}

func redirects(addresses ...string) []RedirectRegistry {
	result := make([]RedirectRegistry, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, RedirectRegistry{
			Config: krakenconfig.Config{
				Address: address,
				Security: security.Config{
					EnableHTTPFallback: true,
				},
			},
		})
	}
	return result
}
