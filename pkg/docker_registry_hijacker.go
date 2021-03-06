package pkg

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/pkg/errors"
	"github.com/uber/kraken/lib/backend/registrybackend"
	"github.com/uber/kraken/lib/backend/registrybackend/security"
	"github.com/uber/kraken/utils/httputil"

	log "github.com/sirupsen/logrus"
)

// DockerRegistryHijacker is an implementation of MitmHijacker to be used to hijack queries to
// docker registries, and redirect them to Kraken.
type DockerRegistryHijacker struct {
	registries []*hijackedRegistry
}

type hijackedRegistry struct {
	*registryClient
	matchingRegex *regexp.Regexp
	redirects     []*redirectRegistry
}

type registryClient struct {
	*registrybackend.Config
	authenticator security.Authenticator
}

type redirectRegistry struct {
	*registryClient
	rewriteRepositories string
}

func newRegistryClient(config registrybackend.Config) (*registryClient, error) {
	authenticator, err := authenticatorFactory(config)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to build authenticator")
	}

	return &registryClient{
		Config:        &config,
		authenticator: authenticator,
	}, nil
}

const (
	manifestQuery registryQueryType = "manifest"
	blobQuery     registryQueryType = "blob"
)

type registryQueryType string

var (
	_ MitmHijacker = &DockerRegistryHijacker{}

	// $1 is the repository,
	// $2 is the query type,
	// $3 is the tag.
	routeRegex = regexp.MustCompile(fmt.Sprintf("^/v2/(.+)/(%s)s/(.+)$",
		strings.Join([]string{string(manifestQuery), string(blobQuery)}, "|")))

	// allows overriding in tests.
	authenticatorFactory = func(config registrybackend.Config) (security.Authenticator, error) {
		return config.Authenticator()
	}
)

// returns a *MitmHijacker to be used to hijack queries to docker registries, and redirect them
// to Kraken.
func NewDockerRegistryHijacker(config *Config) (*DockerRegistryHijacker, error) {
	registries, err := buildRegistryWrappers(config)
	if err != nil {
		return nil, err
	}

	return &DockerRegistryHijacker{
		registries: registries,
	}, nil
}

func buildRegistryWrappers(config *Config) ([]*hijackedRegistry, error) {
	registries := make([]*hijackedRegistry, 0, len(config.Registries))

	for _, registry := range config.Registries {
		client, err := newRegistryClient(registry.Config)
		if err != nil {
			return nil, err
		}

		if len(registry.Redirects) == 0 {
			return nil, errors.Errorf("Registry %q does not configure any redirects", registry.Address)
		}

		redirects := make([]*redirectRegistry, 0, len(registry.Redirects))
		for _, redirect := range registry.Redirects {
			redirectClient, err := newRegistryClient(redirect.Config)
			if err != nil {
				return nil, err
			}

			redirects = append(redirects, &redirectRegistry{
				registryClient:      redirectClient,
				rewriteRepositories: redirect.RewriteRepositories,
			})
		}

		wrapper := &hijackedRegistry{
			registryClient: client,
			redirects:      redirects,
		}

		if len(registry.MatchingRegex) != 0 {
			regex, err := regexp.Compile(registry.MatchingRegex)
			if err != nil {
				return nil, errors.Wrapf(err, "unable to compile regex %q", registry.MatchingRegex)
			}

			wrapper.matchingRegex = regex
		}

		registries = append(registries, wrapper)
	}

	return registries, nil
}

func (h *DockerRegistryHijacker) RequestHandler(responseWriter http.ResponseWriter, request *http.Request) (bool, *http.Response, error) {
	if request.Method != "GET" {
		// we don't proxy anything else, let it through
		return false, nil, nil
	}

	path := strings.TrimRight(request.URL.Path, "/")

	if !strings.HasPrefix(path, "/v2") {
		// not a v2 registry request, let it through
		return false, nil, nil
	}

	registry := h.matchingRegistry(request.Host)
	if registry == nil {
		// we don't proxy this registry, let it through
		return false, nil, nil
	}

	if path == "/v2" {
		// initial handshake, we'll handle authentication to these registries ourselves
		responseWriter.WriteHeader(http.StatusOK)
		_, err := responseWriter.Write([]byte("{}"))
		return true, nil, err
	}

	isRegistryQuery, queryType, repository, tag := parseRegistryURLPath(request.URL.Path)

	if !isRegistryQuery {
		// shouldn't happen from image pulls
		log.Warnf("Unexpected non-registry request to %q", request.URL)
		return false, nil, nil
	}

	requestHeaders := make(map[string]string)
	for key := range request.Header {
		requestHeaders[key] = request.Header.Get(key)
	}

	tryRegistry := func(r *registryClient, rewriteRepoRule string) (*http.Response, error) {
		newRepository := rewriteRepository(rewriteRepoRule, repository, tag)

		opts, err := r.authenticator.Authenticate(newRepository)
		if err != nil {
			log.Errorf("unable to authenticate to registry %q: %v", r.Address, err)
			return nil, err
		}
		redirectURL := fmt.Sprintf("http://%s/v2/%s/%ss/%s", r.Address, newRepository, queryType, tag)

		// preserve original request headers
		opts = append(opts, httputil.SendHeaders(requestHeaders),
			httputil.SendTimeout(r.Config.Timeout))

		response, err := httputil.Get(redirectURL, opts...)
		if err != nil {
			log.Warnf("Failed %s request to %s: %v", queryType, redirectURL, err)
		}
		return response, err
	}

	for _, redirect := range registry.redirects {
		response, err := tryRegistry(redirect.registryClient, redirect.rewriteRepositories)
		if err == nil {
			// done
			return true, response, nil
		}
	}

	// unable to get it from any of the redirects, try & get it from the configured
	// repository, otherwise let the proxy do its thing
	response, err := tryRegistry(registry.registryClient, "")
	return true, response, err
}

func rewriteRepository(rewriteRepoRule, repository, tag string) (newRepository string) {
	if rewriteRepoRule == "" {
		// nothing to re-write
		return repository
	}

	newRepository = strings.ReplaceAll(rewriteRepoRule, "%r", repository)
	newRepository = strings.ReplaceAll(newRepository, "%t", tag)

	return newRepository
}

func (h *DockerRegistryHijacker) matchingRegistry(host string) *hijackedRegistry {
	for _, registry := range h.registries {
		if registry.Address == host ||
			registry.matchingRegex != nil && registry.matchingRegex.MatchString(host) {
			log.Debugf("Found matching registry %s for host %q", registry.Address, host)
			return registry
		}
	}
	log.Tracef("No matching registry for host %q", host)
	return nil
}

// we suffix pace metrics with the name of the registry, abd also mark manifests and blob queries as such.
func (h *DockerRegistryHijacker) TransformMetricName(name MitmProxyStatsdMetricName, request *http.Request) string {
	if name != HijackedRequestTransferPace && name != ProxiedRequestTransferPace {
		return string(name)
	}

	newName := string(name) + "." + strings.ReplaceAll(request.Host, ".", "_")

	isRegistryQuery, queryType, _, _ := parseRegistryURLPath(request.URL.Path)
	if isRegistryQuery {
		newName += "." + string(queryType)
	}

	return newName
}

func parseRegistryURLPath(urlPath string) (isRegistryQuery bool, queryType registryQueryType, repository, tag string) {
	match := routeRegex.FindStringSubmatch(urlPath)
	if len(match) != 0 {
		isRegistryQuery = true
		repository, queryType, tag = match[1], registryQueryType(match[2]), match[3]
	}
	return
}
