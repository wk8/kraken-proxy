package pkg

import krakenconfig "github.com/uber/kraken/lib/backend/registrybackend"

type Config struct {
	listenPort int
	ca         *TLSInfo

	registries []Registry
}

type TLSInfo struct {
	CertPath string
	KeyPath  string
}

type Registry struct {
	*krakenconfig.Config

	// Which address to try & redirect to.
	// à-la SSH config, %h is placeholder for the hostname, %p for port, and %r for repository
	// If left empty, no redirection happens.
	redirectAddress string
}

func NewConfig(configPath string) (*Config, error) {
	// TODO wkpo
	return nil, nil
}
