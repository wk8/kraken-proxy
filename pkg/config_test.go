package pkg

import (
	"io/ioutil"
	"os"
	"testing"
	"time"

	dockertypes "github.com/docker/engine-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	krakenconfig "github.com/uber/kraken/lib/backend/registrybackend"
	"github.com/uber/kraken/lib/backend/registrybackend/security"
)

func TestNewConfig(t *testing.T) {
	sampleConfig := `
listen_address: :2828
ca:
  cert_path: /path/to/cert
  key_path: /path/to/key
log_level: trace
statsd:
  address: 127.0.0.1:9125
  prefix: kraken-proxy
  flush_interval: 10m
  flush_bytes: 1024
registries:
  - address: docker.io
    timeout: 60s
    redirect_address: redirect.me:876
    security:
      basic:
        username: user
        password: pwd
  - address: localhost:7878
    redirect_address: redirect.me.too
`

	tmpFile, err := ioutil.TempFile("", "")
	require.NoError(t, err)
	_, err = tmpFile.WriteString(sampleConfig)
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())
	defer os.RemoveAll(tmpFile.Name())

	config, err := NewConfig(tmpFile.Name())
	require.NoError(t, err)

	expectedConfig := &Config{
		ListenAddress: ":2828",
		CA: &TLSInfo{
			CertPath: "/path/to/cert",
			KeyPath:  "/path/to/key",
		},
		LogLevel: "trace",
		Statsd: &StatsdConfig{
			Address:       "127.0.0.1:9125",
			Prefix:        "kraken-proxy",
			FlushInterval: 10 * time.Minute,
			FlushBytes:    1024,
		},
		Registries: []Registry{
			{
				Config: krakenconfig.Config{
					Address: "docker.io",
					Timeout: 60 * time.Second,
					Security: security.Config{
						BasicAuth: &dockertypes.AuthConfig{
							Username: "user",
							Password: "pwd",
						},
					},
				},
				RedirectAddress: "redirect.me:876",
			},
			{
				Config: krakenconfig.Config{
					Address: "localhost:7878",
				},
				RedirectAddress: "redirect.me.too",
			},
		},
	}

	assert.Equal(t, expectedConfig, config)
}
