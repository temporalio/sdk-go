package main

import (
	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/cadence"
	"code.uber.internal/devexp/minions-client-go.git/examples"
	"code.uber.internal/go-common.git/x/config"
	"code.uber.internal/go-common.git/x/jaeger"
	"code.uber.internal/go-common.git/x/log"
	"github.com/uber/tchannel-go/thrift"
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

	// TChannel to production.
	tchan, err := cfg.TChannel.NewClient("example-client", nil)
	if err != nil {
		log.Panicf("Failed to get a client for the uber-minions: %s\n", err.Error())
	}
	tclient := thrift.NewClient(tchan, "uber-minions", nil)
	service := m.NewTChanWorkflowServiceClient(tclient)

	// Start the necessary Workers.
	helper := examples.NewWorkflowHelper(service)
	helper.StartWorkers()

	// Launch one of each example against the minions service.
	helper.StartWorkflow("greetingsWorkflow")

	// TODO:: Once we have a visibility API replace this untill the workflow completes.
	// helper.StopWorkers()
}
