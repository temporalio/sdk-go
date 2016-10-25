package main

import (
	"code.uber.internal/go-common.git/x/config"
	"code.uber.internal/go-common.git/x/jaeger"
	"code.uber.internal/go-common.git/x/log"
)

func main() {
	var cfg appConfig
	if err := config.Load(&cfg); err != nil {
		log.Fatalf("Error initializing configuration: %s", err)
	}
	log.Configure(&cfg.Logging, cfg.Verbose)
	log.ConfigureSentry(&cfg.Sentry)

	metrics, err := cfg.Metrics.New()
	if err != nil {
		log.Fatalf("Could not connect to metrics: %v", err)
	}
	metrics.Counter("boot").Inc(1)

	closer, err := xjaeger.InitGlobalTracer(cfg.Jaeger, cfg.ServiceName, metrics)
	if err != nil {
		log.Fatalf("Jaeger.InitGlobalTracer failed: %v", err)
	}
	defer closer.Close()
}
