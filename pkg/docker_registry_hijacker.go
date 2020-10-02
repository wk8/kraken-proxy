package pkg

import (
	"net/http"
	"regexp"
	"strings"
)

// DockerRegistryHijacker is an implementation of MitmHijacker to be used to hijack queries to
// docker registries, and redirect them to Kraken.
type DockerRegistryHijacker struct {
	*DefaultMitmHijacker

	config *Config
}

var (
	_ MitmHijacker = &DockerRegistryHijacker{}

	routeRegex = regexp.MustCompile("^/v2/(.+)/(manifests|blobs)/(.+)$")
)

// returns a *MitmHijacker to be used to hijack queries to docker registries, and redirect them
// to Kraken.
func NewDockerRegistryHijacker(config *Config) *DockerRegistryHijacker {
	return &DockerRegistryHijacker{
		DefaultMitmHijacker: &DefaultMitmHijacker{},
		config:              config,
	}
}

// we suffix pace metrics with the name of the registry plus mark manifests and blob queries.
func (h *DockerRegistryHijacker) TransformMetricName(name MitmProxyStatsdMetricName, request *http.Request) string {
	if name != MitMHijackedRequestTransferPace && name != MitMProxyedRequestTransferPace {
		return h.DefaultMitmHijacker.TransformMetricName(name, request)
	}

	newName := string(name) + "." + strings.ReplaceAll(request.Host, ".", "_")

	match := routeRegex.FindStringSubmatch(request.URL.Path)
	if len(match) != 0 {
		newName += "." + match[2]
	}

	return newName
}
