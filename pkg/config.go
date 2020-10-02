package pkg

import (
	"github.com/pkg/errors"
	krakenconfig "github.com/uber/kraken/lib/backend/registrybackend"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"time"
)

type Config struct {
	ListenAddress string        `yaml:"listen_address"`
	CA            *TLSInfo      `yaml:"ca"`
	Statsd        *StatsdConfig `yaml:"statsd"`

	Registries []Registry `yaml:"registries"`
}

type TLSInfo struct {
	CertPath string `yaml:"cert_path"`
	KeyPath  string `yaml:"key_path"`
}

type StatsdConfig struct {
	Address       string        `yaml:"address"`
	Prefix        string        `yaml:"prefix"`
	FlushInterval time.Duration `yaml:"flush_interval"`
	FlushBytes    int           `yaml:"flush_bytes"`
}

type Registry struct {
	krakenconfig.Config `yaml:",inline"`

	// Which address to try & redirect to.
	// à-la SSH config, %h is placeholder for the hostname, %p for port, and %r for repository
	// If left empty, no redirection happens.
	RedirectAddress string `yaml:"redirect_address"`
}

func NewConfig(configPath string) (*Config, error) {
	bytes, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to read file %q", configPath)
	}

	config := &Config{}
	if err := yaml.Unmarshal(bytes, config); err != nil {
		return nil, errors.Wrapf(err, "%q is not a YAML file", configPath)
	}

	return config, nil
}
