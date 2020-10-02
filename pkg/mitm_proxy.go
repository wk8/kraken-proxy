package pkg

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/cactus/go-statsd-client/statsd"
	"github.com/kr/mitm"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// The names of the statsd metrics that MitmProxys push
const (
	// Statsd counter metric incremented when a request is hijacked to a direct reply
	MitMHijackedToDirectReplyCounter = "mitm.hijacked.direct_reply.count"

	// Statsd counter metric incremented when a request is successfully hijacked to a different request
	MitMSuccessfullyHijackedToRequestCounter = "mitm.hijacked.request.success.count"

	// Statsd counter metric incremented when a request is hijacked to a different request, but that request fails
	MitMFailedHijackedToRequestCounter = "mitm.hijacked.request.failure.count"

	// Statsd counter metric incremented when a request is transparently proxy-ed
	MitMProxyedRequestCounter = "mitm.proxyed.count"

	// Statsd timing metric, measuring the time needed to transmit 1kB for successfully hijacked requests
	MitMHijackedRequestTransferPace = "mitm.hijacked.pace"

	// Statsd timing metric, measuring the time needed to transmit 1kB for proxy-ed requests
	MitMProxyedRequestTransferPace = "mitm.proxyed.pace"
)

type MitmProxyStatsdMetricName string

type MitmProxy struct {
	listenAddr   string
	ca           *TLSInfo
	hijacker     MitmHijacker
	statsdClient statsd.StatSender

	server *http.Server
}

// a MitmHijacker tells a MitmProxy how to handle incoming requests
type MitmHijacker interface {
	// RequestHandler is called for all incoming requests
	// * if it returns a true boolean, then it means it has already replied, and the proxy shouldn't do anything
	// * otherwise, if it returns a non-nil request, then the proxy should make that request, and if successful, use the response
	//   instead of getting it from upstream (the hijacker can optionally return an http client to make the request with)
	// * if it returns false, nil, then the proxy just forwards the request upstream
	RequestHandler(http.ResponseWriter, *http.Request) (bool, *http.Request, *http.Client)

	// if the hijacker redirects a request to a modified request, this callback says whether the response
	// obtained from the hijacked request is acceptable, or if the proxy should forward upstream
	AcceptHijackedResponse(*http.Response) bool

	// hijackers can choose to transform statsd metrics' names
	// metricName is guaranteed to be one of the constants defined above.
	// If it returns an empty string, then the metric point is not emitted.
	TransformMetricName(MitmProxyStatsdMetricName, *http.Request) string
}

// a default implementation of the MitmHijacker interface
type DefaultMitmHijacker struct{}

var _ MitmHijacker = &DefaultMitmHijacker{}

func (d DefaultMitmHijacker) RequestHandler(_ http.ResponseWriter, _ *http.Request) (bool, *http.Request, *http.Client) {
	return false, nil, nil
}

func (d DefaultMitmHijacker) AcceptHijackedResponse(response *http.Response) bool {
	return response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
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

// Start is a blocking call
func (p *MitmProxy) Start() error {
	return p.start(nil, nil)
}

// If passed a listeningChan, it will close it when it's started listening
func (p *MitmProxy) start(listeningChan chan interface{}, upstreamTLSConfig *tls.Config) error {
	if p.server != nil {
		return fmt.Errorf("proxy already started")
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
	if err := startHttpServer(p.server, listeningChan, nil, startedLogLine); err != nil {
		return err
	}

	log.Infof("Proxy closed")
	return nil
}

func startHttpServer(server *http.Server, listeningChan chan interface{}, tlsInfo *TLSInfo, startedLogLine string) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	if listeningChan != nil {
		close(listeningChan)
	}

	if startedLogLine != "" {
		log.Infof(startedLogLine)
	}

	if tlsInfo == nil {
		err = server.Serve(listener)
	} else {
		err = server.ServeTLS(listener, tlsInfo.CertPath, tlsInfo.KeyPath)
	}

	if err == http.ErrServerClosed {
		return nil
	}
	return err
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

	// cache this for logging purposes, as the hijacker is allowed to modify the request object
	requestStr := requestToString(request)
	log.Tracef("Request headers for %s: %v", requestStr, request.Header)

	replied, modifiedRequest, client := p.hijacker.RequestHandler(wrapper, request)

	if log.IsLevelEnabled(log.DebugLevel) {
		logLine := fmt.Sprintf("Handling new request to %s: ", requestStr)

		if replied {
			logLine += fmt.Sprintf("hijacker replied (status code %d)", wrapper.statusCode)
		} else if modifiedRequest != nil {
			logLine += fmt.Sprintf("hijacker redirecting to %s", requestToString(modifiedRequest))
		} else {
			logLine += "forwarding upstream"
		}

		log.Debug(logLine)
	}

	if replied {
		p.incrementMetricCounter(MitMHijackedToDirectReplyCounter, request)
		return
	}

	proxyed := true
	defer func() {
		elapsed := time.Since(startedAt)

		log.Tracef("Replied to %s, transmitted %d bytes in %v", requestStr, wrapper.written, elapsed)

		if wrapper.written < 1000 {
			// less than 1 kb of data was transmitted, not relevant to report the pace
			return
		}

		var paceMetricName MitmProxyStatsdMetricName
		if proxyed {
			paceMetricName = MitMProxyedRequestTransferPace
		} else {
			paceMetricName = MitMHijackedRequestTransferPace
		}

		pace := elapsed / time.Duration(wrapper.written/1000)

		p.reportMetricDuration(paceMetricName, request, pace)
	}()

	if modifiedRequest != nil {
		if client == nil {
			client = http.DefaultClient
		}
		response, err := client.Do(modifiedRequest)
		if response != nil {
			defer func() {
				if err := response.Body.Close(); err != nil {
					log.Warnf("Error closing HTTP response: %v", err)
				}
			}()
		}

		if err == nil {
			if p.hijacker.AcceptHijackedResponse(response) {
				log.Debugf("Successfully hijacked request to %s", requestStr)

				headers := wrapper.Header()
				for key, value := range response.Header {
					headers[key] = value
				}
				wrapper.WriteHeader(response.StatusCode)

				if _, err := io.Copy(wrapper, response.Body); err != nil {
					log.Errorf("Unable to write hijacked response body back to client: %v", err)
				}

				p.incrementMetricCounter(MitMSuccessfullyHijackedToRequestCounter, request)
				proxyed = false
				return
			}

			p.incrementMetricCounter(MitMFailedHijackedToRequestCounter, request)
			log.Debugf("Hijacked response to %s not deemed acceptable", requestToString(modifiedRequest))
		} else {
			p.incrementMetricCounter(MitMFailedHijackedToRequestCounter, request)
			log.Errorf("Unable to make hijacked request to %s: %v", requestToString(modifiedRequest), err)
		}
	}

	// if we end up here, means we didn't reply already, didn't hijack, or failed to do so
	// in all these cases, we just forward upstream
	upstream.ServeHTTP(wrapper, request)

	p.incrementMetricCounter(MitMProxyedRequestCounter, request)
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
