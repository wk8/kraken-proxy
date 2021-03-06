package pkg

import (
	"io/ioutil"
	"time"

	"github.com/pkg/errors"
	krakenconfig "github.com/uber/kraken/lib/backend/registrybackend"
	"gopkg.in/yaml.v2"
)

type Config struct {
	ListenAddress string        `yaml:"listen_address"`
	CA            *TLSInfo      `yaml:"ca"`
	LogLevel      string        `yaml:"log_level"`
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

	// if specified, that will be used instead of the registry's address to determine
	// if a given request is addressed to this registry
	MatchingRegex string `yaml:"matching_regex"`

	// which registries to try & redirect to, in order
	Redirects []RedirectRegistry `yaml:"redirects"`
}

type RedirectRegistry struct {
	krakenconfig.Config `yaml:",inline"`

	// if specified, this should indicate how to rewrite repositories
	// à la SSH config, %r will be replaced by the original repository name,
	// and %t by the original tag name
	RewriteRepositories string `yaml:"rewrite_repositories"`
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
