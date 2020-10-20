package pkg

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cactus/go-statsd-client/statsd"
	"github.com/kr/mitm"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

// The names of the statsd metrics that MitmProxys push.
const (
	// Statsd counter metric incremented when a request is hijacked.
	HijackedRequestCounter MitmProxyStatsdMetricName = "mitm.hijacked"

	// Statsd counter metric incremented when a request is transparently proxied.
	ProxiedRequestCounter MitmProxyStatsdMetricName = "mitm.proxied"

	// Statsd timing metric, measuring the time needed to transmit 1kB for hijacked requests.
	HijackedRequestTransferPace MitmProxyStatsdMetricName = "mitm.hijacked.pace"

	// Statsd timing metric, measuring the time needed to transmit 1kB for proxied requests.
	ProxiedRequestTransferPace MitmProxyStatsdMetricName = "mitm.proxied.pace"

	// Statsd counter metric incremented when hijacking a request fails.
	HijackingErrorsCounter MitmProxyStatsdMetricName = "mitm.hijacked.errors"

	oneKb = 1000
)

type MitmProxyStatsdMetricName string

type MitmProxy struct {
	listenAddr   string
	ca           *TLSInfo
	hijacker     MitmHijacker
	statsdClient statsd.StatSender

	server *http.Server
}

// a MitmHijacker tells a MitmProxy how to handle incoming requests.
type MitmHijacker interface {
	// RequestHandler is called for all incoming requests
	// * the first item of the return tuple, the boolean, says whether the hijacker wishes to hijack that request; in
	//   that case,
	// * if that first item is true, the hijacker can optionally provide a *http.Response to copy to the client
	// * if the first item of the return tuple is false, or error is not nil, then the proxy forwards the request upstream
	RequestHandler(http.ResponseWriter, *http.Request) (bool, *http.Response, error)

	// hijackers can choose to transform statsd metrics' names
	// metricName is guaranteed to be one of the constants defined above.
	// If it returns an empty string, then the metric point is not emitted.
	TransformMetricName(MitmProxyStatsdMetricName, *http.Request) string
}

// A default implementation of the MitmHijacker interface.
type DefaultMitmHijacker struct{}

var _ MitmHijacker = &DefaultMitmHijacker{}

func (d DefaultMitmHijacker) RequestHandler(http.ResponseWriter, *http.Request) (bool, *http.Response, error) {
	return false, nil, nil
}

func (d DefaultMitmHijacker) TransformMetricName(name MitmProxyStatsdMetricName, _ *http.Request) string {
	return string(name)
}

func NewMitmProxy(listenAddr string, ca *TLSInfo, hijacker MitmHijacker, statsdClient statsd.StatSender) *MitmProxy {
	if hijacker == nil {
		hijacker = &DefaultMitmHijacker{}
	}

	return &MitmProxy{
		listenAddr:   listenAddr,
		ca:           ca,
		hijacker:     hijacker,
		statsdClient: statsdClient,
	}
}

// Start is a blocking call.
func (p *MitmProxy) Start() error {
	return p.start(nil, nil)
}

// If passed a listeningChan, it will close it when it's started listening.
func (p *MitmProxy) start(listeningChan chan interface{}, upstreamTLSConfig *tls.Config) error {
	if p.server != nil {
		return errors.New("proxy already started")
	}

	ca, err := p.loadCA()
	if err != nil {
		return errors.Wrap(err, "unable to load TLSInfo")
	}

	p.server = &http.Server{
		Addr: p.listenAddr,
		Handler: &mitm.Proxy{
			CA: &ca,
			Wrap: func(upstream http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					p.RequestHandler(upstream, writer, request)
				})
			},
			TLSClientConfig: upstreamTLSConfig,
		},
	}

	startedLogLine := fmt.Sprintf("Proxy listening on %s", p.listenAddr)
	if err := startHTTPServer(p.server, listeningChan, nil, startedLogLine); err != nil {
		return err
	}

	log.Infof("Proxy closed")
	return nil
}

type writerWrapper struct {
	http.ResponseWriter
	written    int64
	statusCode int
}

func (w *writerWrapper) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
	w.statusCode = code
}

func (w *writerWrapper) Write(data []byte) (int, error) {
	written, err := w.ResponseWriter.Write(data)
	w.written += int64(written)
	return written, err
}

func (p *MitmProxy) RequestHandler(upstream http.Handler, writer http.ResponseWriter, request *http.Request) {
	startedAt := time.Now()
	wrapper := &writerWrapper{ResponseWriter: writer}

	requestStr := requestToString(request)
	log.Tracef("Request headers for %s: %v", requestStr, request.Header)

	hijacked, response, err := p.hijacker.RequestHandler(wrapper, request)
	if err != nil {
		log.Errorf("Error from hijacker when handling request %s, forwarding upstream: %v", requestStr, err)
		p.incrementMetricCounter(HijackingErrorsCounter, request)
		hijacked = false
	}

	defer func() {
		elapsed := time.Since(startedAt)

		var logVerb string
		if hijacked {
			p.incrementMetricCounter(HijackedRequestCounter, request)
			logVerb = "Hijacked"
		} else {
			p.incrementMetricCounter(ProxiedRequestCounter, request)
			logVerb = "Proxied"
		}

		log.Tracef("%s request %s, transmitted %d bytes in %v", logVerb, requestStr, wrapper.written, elapsed)

		if wrapper.written < oneKb {
			// less than 1 kb of data was transmitted, not relevant to report the pace
			return
		}

		var paceMetricName MitmProxyStatsdMetricName

		if hijacked {
			paceMetricName = HijackedRequestTransferPace
		} else {
			paceMetricName = ProxiedRequestTransferPace
		}

		pace := elapsed / time.Duration(wrapper.written/oneKb)

		p.reportMetricDuration(paceMetricName, request, pace)
	}()

	if hijacked && response != nil {
		defer func() {
			if err := response.Body.Close(); err != nil {
				log.Warnf("Error closing HTTP response: %v", err)
			}
		}()

		headers := wrapper.Header()
		for key, value := range response.Header {
			headers[key] = value
		}
		wrapper.WriteHeader(response.StatusCode)

		if _, err := io.Copy(wrapper, response.Body); err != nil {
			log.Errorf("Unable to write hijacked response body back to client: %v", err)
		}
	} else if !hijacked {
		upstream.ServeHTTP(wrapper, request)
	}
}

func (p *MitmProxy) incrementMetricCounter(metricName MitmProxyStatsdMetricName, request *http.Request) {
	if metricNameStr := p.metricName(metricName, request); metricNameStr != "" {
		if err := p.statsdClient.Inc(metricNameStr, 1, 1); err != nil {
			log.Warnf("Unable to increment metric counter %q: %v", metricNameStr, err)
		}
	}
}

func (p *MitmProxy) reportMetricDuration(metricName MitmProxyStatsdMetricName, request *http.Request, d time.Duration) {
	if metricNameStr := p.metricName(metricName, request); metricNameStr != "" {
		if err := p.statsdClient.TimingDuration(metricNameStr, d, 1); err != nil {
			log.Warnf("Unable to report metric duration %q: %v", metricNameStr, err)
		}
	}
}

func (p *MitmProxy) metricName(metricName MitmProxyStatsdMetricName, request *http.Request) string {
	if p.statsdClient == nil {
		return ""
	}
	return strings.TrimSpace(p.hijacker.TransformMetricName(metricName, request))
}

func (p *MitmProxy) loadCA() (cert tls.Certificate, err error) {
	cert, err = tls.LoadX509KeyPair(p.ca.CertPath, p.ca.KeyPath)
	if err == nil {
		cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	}
	return
}

func (p *MitmProxy) Stop() error {
	if p.server == nil {
		return errors.New("Proxy not started yet")
	}
	return p.server.Shutdown(context.Background())
}

func requestToString(request *http.Request) string {
	return fmt.Sprintf("%s \"%s%v\"", request.Method, request.Host, request.URL)
}
