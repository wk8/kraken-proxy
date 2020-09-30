package main

import (
	"fmt"
	flags "github.com/jessevdk/go-flags"
	log "github.com/sirupsen/logrus"
	"github.com/wk8/kraken-proxy/pkg"
	"os"
	"path"
)

var opts struct {
	LogLevel   string `long:"log-level" env:"LOG_LEVEL" description:"Log level" default:"info"`
	ConfigPath string `long:"config" env:"CONFIG" description:"Path to config" default:"config.yml"`
}

func main() {
	parseArgs()
	initLogging()

	config, err := pkg.NewConfig(opts.ConfigPath)
	if err != nil {
		log.Fatalf("unable to parse config %q: %v", opts.ConfigPath, err)
	}
	fmt.Println(config) // TODO wkpo

	// TODO wkpo oldies
	ca := &pkg.TLSInfo{
		CertPath: certFile,
		KeyPath:  keyFile,
	}
	proxy := pkg.NewMitmProxy(":59002", ca, nil, nil)
	fmt.Println(proxy.Start())
}

// TODO wkpo remove!
var (
	dir      = path.Join(os.Getenv("HOME"), ".mitm")
	keyFile  = path.Join(dir, "ca-key.pem")
	certFile = path.Join(dir, "ca-cert.pem")
)

func parseArgs() {
	parser := flags.NewParser(&opts, flags.Default)
	if _, err := parser.Parse(); err != nil {
		// If the error was from the parser, then we can simply return
		// as Parse() prints the error already
		if _, ok := err.(*flags.Error); ok {
			os.Exit(1)
		}
		log.Fatalf("Error parsing flags: %v", err)
	}
}

func initLogging() {
	level, err := log.ParseLevel(opts.LogLevel)
	if err != nil {
		log.Fatalf("Unknown log level %s: %v", opts.LogLevel, err)
	}
	log.SetLevel(level)

	// Set the log format to have a reasonable timestamp
	formatter := &log.TextFormatter{
		FullTimestamp: true,
	}
	log.SetFormatter(formatter)
}
