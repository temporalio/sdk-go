package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
	resolveTimeout      = 10 * time.Second
)

func main() {
	resolve := flag.Bool("resolve-default", false, "emit the default CLI archive and cache fingerprint for GitHub Actions")
	defaultOnly := flag.Bool("default-only", false, "prefetch only the SDK default CLI")
	flag.Parse()

	if *resolve {
		if err := resolveDefault(); err != nil {
			log.Fatal(err)
		}
		return
	}
	versions := []string{"default"}
	if !*defaultOnly {
		versions = append(versions, temporalcli.IntegrationServerVersion)
	}
	if err := prefetch(versions); err != nil {
		log.Fatal(err)
	}
}

func resolveDefault() error {
	archive, err := retry("resolving the default Temporal CLI", resolveTimeout, func(ctx context.Context) (temporalcli.Archive, error) {
		return temporalcli.Resolve(ctx, "default", sdklog.NewNopLogger())
	})
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(archive)
	if err != nil {
		return err
	}
	log.Printf("Resolved default Temporal CLI archive: %s", archive.ArchiveURL)
	_, err = fmt.Printf("fingerprint=%x\narchive=%s\n", sha256.Sum256(metadata), metadata)
	return err
}

func prefetch(versions []string) error {
	var defaultArchive temporalcli.Archive
	if err := json.Unmarshal([]byte(os.Getenv("CLI_DEFAULT_ARCHIVE")), &defaultArchive); err != nil {
		return fmt.Errorf("reading resolved default Temporal CLI archive: %w", err)
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	logger := sdklog.NewDefaultLogger()
	for _, version := range versions {
		path, err := retry("prefetching Temporal CLI "+version, downloadTimeout, func(ctx context.Context) (string, error) {
			if version == "default" {
				return defaultArchive.DownloadDefault(ctx, cacheDir, logger)
			}
			return temporalcli.Download(ctx, version, cacheDir, logger)
		})
		if err != nil {
			return err
		}
		if err := installCLI(path); err != nil {
			return fmt.Errorf("installing Temporal CLI %s: %w", version, err)
		}
	}
	return nil
}

func retry[T any](operation string, timeout time.Duration, run func(context.Context) (T, error)) (T, error) {
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		value, err := run(ctx)
		cancel()
		if err == nil {
			return value, nil
		}
		if attempt == maxDownloadAttempts {
			return value, fmt.Errorf("%s: %w", operation, err)
		}
		log.Printf("%s, attempt %d failed: %v", operation, attempt, err)
		time.Sleep(time.Duration(1<<attempt) * time.Second)
	}
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
