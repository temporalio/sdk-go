// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
package sysinfo

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var Timeout = 3 * time.Second

// EnvKey is the type for context keys used to pass environment variables.
type EnvKey string

// EnvKeyType is the type alias for environment variable keys.
type EnvKeyType = string

// EnvMap is the type alias for environment variable maps.
type EnvMap = map[EnvKeyType]string

// ReadLines reads contents from a file and splits them by new lines.
func ReadLines(filename string) ([]string, error) {
	return ReadLinesOffsetN(filename, 0, -1)
}

// ReadLinesOffsetN reads contents from file and splits them by new line.
// The offset tells at which line number to start.
// The count determines the number of lines to read (starting from offset):
// n >= 0: at most n lines
// n < 0: whole file
func ReadLinesOffsetN(filename string, offset uint, n int) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return []string{""}, err
	}
	defer f.Close()

	var ret []string

	r := bufio.NewReader(f)
	for i := uint(0); i < uint(n)+offset || n < 0; i++ {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && len(line) > 0 {
				ret = append(ret, strings.Trim(line, "\n"))
			}
			break
		}
		if i < offset {
			continue
		}
		ret = append(ret, strings.Trim(line, "\n"))
	}

	return ret, nil
}

// GetEnvWithContext retrieves the environment variable key.
// If it does not exist it returns the default.
func GetEnvWithContext(ctx context.Context, key string, dfault string, combineWith ...string) string {
	var value string
	if env, ok := ctx.Value(EnvKey("env")).(EnvMap); ok {
		value = env[key]
	}
	if value == "" {
		value = os.Getenv(key)
	}
	if value == "" {
		value = dfault
	}

	return combine(value, combineWith)
}

func combine(value string, combineWith []string) string {
	switch len(combineWith) {
	case 0:
		return value
	case 1:
		return filepath.Join(value, combineWith[0])
	default:
		all := make([]string, len(combineWith)+1)
		all[0] = value
		copy(all[1:], combineWith)
		return filepath.Join(all...)
	}
}

// HostProcWithContext returns the path to /proc (or HOST_PROC if set).
func HostProcWithContext(ctx context.Context, combineWith ...string) string {
	return GetEnvWithContext(ctx, "HOST_PROC", "/proc", combineWith...)
}

// PathExists checks if a path exists.
func PathExists(filename string) bool {
	if _, err := os.Stat(filename); err == nil {
		return true
	}
	return false
}

// Sleep sleeps for the specified duration, respecting context cancellation.
func Sleep(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
