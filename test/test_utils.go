package test

import (
	"os"
	"strings"

	"go.temporal.io/temporal/client"
)

// Config contains the integration test configuration
type Config struct {
	ServiceAddr string
	IsStickyOff bool
	Debug       bool
}

func newConfig() Config {
	cfg := Config{
		ServiceAddr: client.DefaultHostPort,
		IsStickyOff: true,
	}
	if addr := getEnvServiceAddr(); addr != "" {
		cfg.ServiceAddr = addr
	}
	if so := getEnvStickyOff(); so != "" {
		cfg.IsStickyOff = so == "true"
	}
	if debug := getDebug(); debug != "" {
		cfg.Debug = debug == "true"
	}
	return cfg
}

func getEnvServiceAddr() string {
	return strings.TrimSpace(os.Getenv("SERVICE_ADDR"))
}

func getEnvStickyOff() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("STICKY_OFF")))
}

func getDebug() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("DEBUG")))
}
