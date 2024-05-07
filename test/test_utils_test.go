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
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pborman/uuid"
	"google.golang.org/protobuf/types/known/durationpb"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/workflow"
)

type (
	// Config contains the integration test configuration
	Config struct {
		ServiceAddr             string
		ServiceHTTPAddr         string
		maxWorkflowCacheSize    int
		Debug                   bool
		Namespace               string
		ShouldRegisterNamespace bool
		TLS                     *tls.Config
	}
	// context.WithValue need this type instead of basic type string to avoid lint error
	contextKey               string
	ConfigAndClientSuiteBase struct {
		config        Config
		client        client.Client
		taskQueueName string
	}
)

var taskQueuePrefix = "tq-" + uuid.New()

// NewConfig creates new Config instance
func NewConfig() Config {
	cfg := Config{
		ServiceAddr:             client.DefaultHostPort,
		ServiceHTTPAddr:         "localhost:7243",
		maxWorkflowCacheSize:    10000,
		Namespace:               "integration-test-namespace",
		ShouldRegisterNamespace: true,
	}
	if addr := getEnvServiceAddr(); addr != "" {
		cfg.ServiceAddr = addr
	}
	if addr := strings.TrimSpace(os.Getenv("SERVICE_HTTP_ADDR")); addr != "" {
		cfg.ServiceHTTPAddr = addr
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
	if os.Getenv("TEMPORAL_NAMESPACE") != "" {
		cfg.Namespace = os.Getenv("TEMPORAL_NAMESPACE")
		cfg.ShouldRegisterNamespace = false
	}
	if os.Getenv("TEMPORAL_CLIENT_CERT") != "" || os.Getenv("TEMPORAL_CLIENT_KEY") != "" {
		log.Print("Using custom client certificate")
		cert, err := tls.X509KeyPair([]byte(os.Getenv("TEMPORAL_CLIENT_CERT")), []byte(os.Getenv("TEMPORAL_CLIENT_KEY")))
		if err != nil {
			panic(fmt.Sprintf("Failed loading client cert: %v", err))
		}
		cfg.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
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

func (ts *ConfigAndClientSuiteBase) InitConfigAndNamespace() error {
	ts.config = NewConfig()
	var err error
	err = WaitForTCP(time.Minute, ts.config.ServiceAddr)
	if err != nil {
		return err
	}
	if ts.config.ShouldRegisterNamespace {
		if err = ts.registerNamespace(); err != nil {
			return err
		} else if err = ts.ensureSearchAttributes(); err != nil {
			return err
		}
	}
	return nil
}

func (ts *ConfigAndClientSuiteBase) InitClient() error {
	var err error
	if ts.client != nil {
		return nil
	}
	ts.client, err = ts.newClient()
	return err
}

func (ts *ConfigAndClientSuiteBase) newClient() (client.Client, error) {
	return client.Dial(client.Options{
		HostPort:          ts.config.ServiceAddr,
		Namespace:         ts.config.Namespace,
		Logger:            ilog.NewDefaultLogger(),
		ConnectionOptions: client.ConnectionOptions{TLS: ts.config.TLS},
	})
}

func SimplestWorkflow(_ workflow.Context) error {
	return nil
}

func (ts *ConfigAndClientSuiteBase) registerNamespace() error {
	client, err := client.NewNamespaceClient(client.Options{
		HostPort:          ts.config.ServiceAddr,
		ConnectionOptions: client.ConnectionOptions{TLS: ts.config.TLS},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	err = client.Register(ctx, &workflowservice.RegisterNamespaceRequest{
		Namespace:                        ts.config.Namespace,
		WorkflowExecutionRetentionPeriod: durationpb.New(1 * 24 * time.Hour),
	})
	defer client.Close()
	if _, ok := err.(*serviceerror.NamespaceAlreadyExists); ok {
		return nil
	}
	if err != nil {
		return err
	}
	time.Sleep(namespaceCacheRefreshInterval) // wait for namespace cache refresh on temporal-server
	err = ts.InitClient()
	if err != nil {
		return err
	}
	// below is used to guarantee namespace is ready
	var dummyReturn string
	err = ts.executeWorkflow("test-namespace-exist", SimplestWorkflow, &dummyReturn)
	numOfRetry := 20
	for err != nil && numOfRetry >= 0 {
		if _, ok := err.(*serviceerror.NamespaceNotFound); ok {
			time.Sleep(namespaceCacheRefreshInterval)
			err = ts.executeWorkflow("test-namespace-exist", SimplestWorkflow, &dummyReturn)
		} else {
			break
		}
		numOfRetry--
	}
	return nil
}

func (ts *ConfigAndClientSuiteBase) ensureSearchAttributes() error {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	// We have to create a client specifically for this call and close it after
	// this call because it may not get closed externally and can trip the
	// goroutine leak detector.
	client, err := ts.newClient()
	if err != nil {
		return err
	}
	defer client.Close()

	// Add CustomKeywordField and CustomStringField attribute if not already present
	saResp, err := client.OperatorService().ListSearchAttributes(ctx, &operatorservice.ListSearchAttributesRequest{
		Namespace: ts.config.Namespace,
	})
	if err != nil {
		return fmt.Errorf("failed checking search attributes: %w", err)
	} else if _, ok := saResp.CustomAttributes["CustomKeywordField"]; ok {
		return nil
	}
	_, err = client.OperatorService().AddSearchAttributes(ctx, &operatorservice.AddSearchAttributesRequest{
		Namespace: ts.config.Namespace,
		SearchAttributes: map[string]enumspb.IndexedValueType{
			"CustomKeywordField": enumspb.INDEXED_VALUE_TYPE_KEYWORD,
			"CustomStringField":  enumspb.INDEXED_VALUE_TYPE_TEXT,
		},
	})
	if err != nil {
		return fmt.Errorf("failed adding search attribute: %w", err)
	}
	return nil
}

// executeWorkflow executes a given workflow and waits for the result
func (ts *ConfigAndClientSuiteBase) executeWorkflow(
	wfID string, wfFunc interface{}, retValPtr interface{}, args ...interface{}) error {
	return ts.executeWorkflowWithOption(ts.startWorkflowOptions(wfID), wfFunc, retValPtr, args...)
}

func (ts *ConfigAndClientSuiteBase) executeWorkflowWithOption(
	options client.StartWorkflowOptions, wfFunc interface{}, retValPtr interface{}, args ...interface{}) error {
	return ts.executeWorkflowWithContextAndOption(context.Background(), options, wfFunc, retValPtr, args...)
}

func (ts *ConfigAndClientSuiteBase) executeWorkflowWithContextAndOption(
	ctx context.Context, options client.StartWorkflowOptions, wfFunc interface{}, retValPtr interface{}, args ...interface{}) error {
	ctx, cancel := context.WithTimeout(ctx, ctxTimeout)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(ctx, options, wfFunc, args...)
	if err != nil {
		return err
	}
	err = run.Get(ctx, retValPtr)
	if ts.config.Debug {
		iter := ts.client.GetWorkflowHistory(ctx, options.ID, run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		for iter.HasNext() {
			event, err1 := iter.Next()
			if err1 != nil {
				break
			}
			fmt.Println(event.String())
		}
	}
	return err
}

func (ts *ConfigAndClientSuiteBase) startWorkflowOptions(wfID string) client.StartWorkflowOptions {
	var wfOptions = client.StartWorkflowOptions{
		ID:                       wfID,
		TaskQueue:                ts.taskQueueName,
		WorkflowExecutionTimeout: 15 * time.Second,
		WorkflowTaskTimeout:      time.Second,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	}
	if wfID == CronWorkflowID {
		wfOptions.CronSchedule = "@every 1s"
	}
	return wfOptions
}
