package pkg

import (
	goerrors "errors"
	"net"
	"net/http"

	log "github.com/sirupsen/logrus"
)

func startHTTPServer(server *http.Server, listeningChan chan interface{}, tlsInfo *TLSInfo, startedLogLine string) error {
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

	if goerrors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
