package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	sdklog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/internal/temporalcli"
)

const (
	cacheDir            = ".build/temporal-cli"
	maxDownloadAttempts = 3
	downloadTimeout     = 2 * time.Minute
)

func main() {
	defaultOnly := flag.Bool("default-only", false, "prefetch only the SDK default CLI")
	flag.Parse()

	versions := []string{"default"}
	if !*defaultOnly {
		versions = append(versions, temporalcli.IntegrationServerVersion)
	}
	if err := prefetch(versions); err != nil {
		log.Fatal(err)
	}
}

func prefetch(versions []string) error {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	logger := sdklog.NewDefaultLogger()
	for _, version := range versions {
		for attempt := 1; ; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
			path, err := temporalcli.Download(ctx, version, cacheDir, logger)
			cancel()
			if err == nil {
				if err := installCLI(path); err != nil {
					return fmt.Errorf("installing Temporal CLI %s: %w", version, err)
				}
				break
			}
			if attempt == maxDownloadAttempts {
				return fmt.Errorf("prefetching Temporal CLI %s: %w", version, err)
			}
			log.Printf("Temporal CLI %s download attempt %d failed: %v", version, attempt, err)
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
	}
	return nil
}

func installCLI(path string) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()

	dest, err := os.CreateTemp(os.TempDir(), "temporal-cli-installing-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(dest.Name()) }()

	_, copyErr := io.Copy(dest, source)
	if err := errors.Join(copyErr, dest.Chmod(0755), dest.Close()); err != nil {
		return err
	}
	return os.Rename(dest.Name(), filepath.Join(os.TempDir(), filepath.Base(path)))
}
