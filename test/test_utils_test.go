// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package test_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

type (
	// Config contains the integration test configuration
	Config struct {
		ServiceAddr          string
		maxWorkflowCacheSize int
		Debug                bool
	}
	// context.WithValue need this type instead of basic type string to avoid lint error
	contextKey string
)

// NewConfig creates new Config instance
func NewConfig() Config {
	cfg := Config{
		ServiceAddr:          client.DefaultHostPort,
		maxWorkflowCacheSize: 10000,
	}
	if addr := getEnvServiceAddr(); addr != "" {
		cfg.ServiceAddr = addr
	}
	if siz := getEnvCacheSize(); siz != "" {
		asInt, err := strconv.Atoi(siz)
		if err != nil {
			panic("Sticky cache size must be an integer, was: " + siz)
		}
		cfg.maxWorkflowCacheSize = asInt
	}
	if debug := getDebug(); debug != "" {
		cfg.Debug = debug == "true"
	}
	return cfg
}

func getEnvServiceAddr() string {
	return strings.TrimSpace(os.Getenv("SERVICE_ADDR"))
}

func getEnvCacheSize() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("WORKFLOW_CACHE_SIZE")))
}

func getDebug() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("DEBUG")))
}

// WaitForTCP waits until target tcp address is available.
func WaitForTCP(timeout time.Duration, addr string) error {
	var d net.Dialer
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("failed to wait until %s: %v", addr, ctx.Err())
		default:
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				continue
			}
			_ = conn.Close()
			return nil
		}
	}
}

// keysPropagator propagates the list of keys across a workflow,
// interpreting the payloads as strings.
// TODO: BORROWED FROM 'internal' PACKAGE TESTS.
// TODO: remove code duplication.
type keysPropagator struct {
	keys []string
}

// NewKeysPropagator returns a context propagator that propagates a set of
// string key-value pairs across a workflow
func NewKeysPropagator(keys []string) workflow.ContextPropagator {
	return &keysPropagator{keys}
}

// Inject injects values from context into headers for propagation
func (s *keysPropagator) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	for _, key := range s.keys {
		value, ok := ctx.Value(contextKey(key)).(string)
		if !ok {
			continue
		}
		encodedValue, err := converter.GetDefaultDataConverter().ToPayload(value)
		if err != nil {
			return err
		}
		writer.Set(key, encodedValue)
	}
	return nil
}

// InjectFromWorkflow injects values from context into headers for propagation
func (s *keysPropagator) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	for _, key := range s.keys {
		value, ok := ctx.Value(contextKey(key)).(string)
		if !ok {
			continue
		}
		encodedValue, err := converter.GetDefaultDataConverter().ToPayload(value)
		if err != nil {
			return err
		}
		writer.Set(key, encodedValue)
	}
	return nil
}

// Extract extracts values from headers and puts them into context
func (s *keysPropagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	for _, key := range s.keys {
		value, ok := reader.Get(key)
		if !ok {
			// If key that should be propagated doesn't exist in the header, ignore the key.
			continue
		}
		var decodedValue string
		err := converter.GetDefaultDataConverter().FromPayload(value, &decodedValue)
		if err != nil {
			return ctx, err
		}
		ctx = context.WithValue(ctx, contextKey(key), decodedValue)
	}
	return ctx, nil
}

// ExtractToWorkflow extracts values from headers and puts them into context
func (s *keysPropagator) ExtractToWorkflow(ctx workflow.Context, reader workflow.HeaderReader) (workflow.Context, error) {
	for _, key := range s.keys {
		value, ok := reader.Get(key)
		if !ok {
			// If key that should be propagated doesn't exist in the header, ignore the key.
			continue
		}
		var decodedValue string
		err := converter.GetDefaultDataConverter().FromPayload(value, &decodedValue)
		if err != nil {
			return ctx, err
		}
		ctx = workflow.WithValue(ctx, contextKey(key), decodedValue)
	}
	return ctx, nil
}
