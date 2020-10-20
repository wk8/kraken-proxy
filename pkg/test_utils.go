package pkg

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// only for tests.
const genericTestTimeout = 5 * time.Second

// getAvailablePort asks the kernel for an available port, that is ready to use - only for tests.
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

func makeRequest(t *testing.T, client *http.Client, baseURL, route string) (response *http.Response, body []byte) {
	if client == nil {
		client = http.DefaultClient
	}

	response, err := client.Get(baseURL + route)
	require.NoError(t, err)

	return response, readResponseBody(t, response)
}

func readResponseBody(t *testing.T, response *http.Response) []byte {
	body, err := ioutil.ReadAll(response.Body)
	require.NoError(t, response.Body.Close())
	require.NoError(t, err)

	return body
}
