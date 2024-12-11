// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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

package internal

import (
	"context"
	"time"

	commonpb "go.temporal.io/api/common/v1"
)

// DeploymentReachability specifies which category of tasks may reach a worker
// associated with a deployment, simplifying safe decommission.
// NOTE: Experimental
type DeploymentReachability int

const (
	// DeploymentReachabilityUnspecified - Reachability level not specified.
	// NOTE: Experimental
	DeploymentReachabilityUnspecified = iota

	// DeploymentReachabilityReachable - The deployment is reachable by new
	// and/or open workflows. The deployment cannot be decommissioned safely.
	// NOTE: Experimental
	DeploymentReachabilityReachable

	// DeploymentReachabilityClosedWorkflows - The deployment is not reachable
	// by new or open workflows, but might be still needed by
	// Queries sent to closed workflows. The deployment can be decommissioned
	// safely if user does not query closed workflows.
	// NOTE: Experimental
	DeploymentReachabilityClosedWorkflows

	// DeploymentReachabilityUnreachable - The deployment is not reachable by
	// any workflow because all the workflows who needed this
	// deployment are out of the retention period. The deployment can be
	// decommissioned safely.
	// NOTE: Experimental
	DeploymentReachabilityUnreachable
)

type (
	// Deployment identifies a set of workers. This identifier combines
	// the deployment series name with their Build ID.
	// NOTE: Experimental
	Deployment struct {
		// SeriesName - Name of the deployment series. Different versions of the same worker
		// service/application are linked together by sharing a series name.
		SeriesName string

		// BuildID - Identifies the worker's code and configuration version.
		BuildID string
	}

	// DeploymentTaskQueueInfo describes properties of the Task Queues involved
	// in a deployment.
	// NOTE: Experimental
	DeploymentTaskQueueInfo struct {
		// Name - Task queue name.
		Name string

		// Type - The type of this task queue.
		Type TaskQueueType

		// FirstPollerTime - Time when the server saw the first poller for this task queue
		// in this deployment.
		FirstPollerTime time.Time
	}

	// DeploymentInfo holds information associated with
	// workers in this deployment.
	// Workers can poll multiple task queues in a single deployment,
	// which are listed in this message.
	// NOTE: Experimental
	DeploymentInfo struct {
		// Deployment - An identifier for this deployment.
		Deployment Deployment

		// CreateTime - When this deployment was created.
		CreateTime time.Time

		// IsCurrent - Whether this deployment is the current one for its deployment series.
		IsCurrent bool

		// TaskQueuesInfos - List of task queues polled by workers in this deployment.
		TaskQueuesInfos []DeploymentTaskQueueInfo

		// Metadata - A user-defined set of key-values. Can be updated with [DeploymentClient.SetCurrent].
		Metadata map[string]*commonpb.Payload
	}

	// DeploymentListEntry is a subset of fields from DeploymentInfo
	// NOTE: Experimental
	DeploymentListEntry struct {
		// An identifier for this deployment.
		Deployment Deployment

		// When this deployment was created.
		CreateTime time.Time

		// Whether this deployment is the current one for its deployment series.
		IsCurrent bool
	}

	// DeploymentListIterator is an iterator for deployments.
	// NOTE: Experimental
	DeploymentListIterator interface {
		// HasNext - Return whether this iterator has next value.
		HasNext() bool

		// Next - Returns the next deployment and error
		Next() (*DeploymentListEntry, error)
	}

	// DeploymentListOptions are the parameters for configuring listing deployments.
	// NOTE: Experimental
	DeploymentListOptions struct {
		// PageSize - How many results to fetch from the Server at a time.
		// Optional: defaulted to 1000
		PageSize int

		// SeriesName - Filter with the name of the deployment series.
		// Optional: If present, use an exact series name match.
		SeriesName string
	}

	// DeploymentReachabilityInfo extends [DeploymentInfo] with reachability information.
	// NOTE: Experimental
	DeploymentReachabilityInfo struct {
		// DeploymentInfo - Information about the deployment.
		DeploymentInfo DeploymentInfo

		// Reachability - Kind of tasks that may reach a worker
		// associated with a deployment.
		Reachability DeploymentReachability

		// LastUpdateTime - When reachability was last computed. Computing reachability
		// is an expensive operation, and the server caches results.
		LastUpdateTime time.Time
	}

	// DeploymentMetadataUpdate modifies user-defined metadata entries that describe
	// a deployment.
	// NOTE: Experimental
	DeploymentMetadataUpdate struct {
		// UpsertEntries - Metadata entries inserted or modified. When values are not
		// of type *commonpb.Payload, the client data converter will be used to generate
		// payloads.
		UpsertEntries map[string]interface{}

		// RemoveEntries - List of keys to remove from the metadata.
		RemoveEntries []string
	}

	// DeploymentSetCurrentOptions provides options for [DeploymentClient.SetCurrent].
	// NOTE: Experimental
	DeploymentSetCurrentOptions struct {
		// Deployment - An identifier for this deployment.
		Deployment Deployment

		// MetadataUpdate - Optional: Changes to the user-defined metadata entries
		// for this deployment.
		MetadataUpdate DeploymentMetadataUpdate
	}

	// DeploymentSetCurrentResponse is the response type for [DeploymentClient.SetCurrent].
	// NOTE: Experimental
	DeploymentSetCurrentResponse struct {
		// Current - Information about the current deployment after this operation.
		Current DeploymentInfo

		// Previous - Information about the last current deployment, i.e., before this operation.
		Previous DeploymentInfo
	}

	// DeploymentDescribeOptions provides options for [DeploymentClient.Describe].
	// NOTE: Experimental
	DeploymentDescribeOptions struct {
		// Deployment - Identifier that combines the deployment series name with their Build ID.
		Deployment Deployment
	}

	// DeploymentGetReachabilityOptions provides options for [DeploymentClient.GetReachability].
	// NOTE: Experimental
	DeploymentGetReachabilityOptions struct {
		// Deployment - Identifier that combines the deployment series name with their Build ID.
		Deployment Deployment
	}

	// DeploymentGetCurrentOptions provides options for [DeploymentClient.GetCurrent].
	// NOTE: Experimental
	DeploymentGetCurrentOptions struct {
		// SeriesName - Name of the deployment series.
		SeriesName string
	}

	// DeploymentClient is the client that manages deployments.
	// NOTE: Experimental
	DeploymentClient interface {
		// Describes an existing deployment.
		// NOTE: Experimental
		Describe(ctx context.Context, options DeploymentDescribeOptions) (DeploymentInfo, error)

		// List returns an iterator to enumerate deployments in the client's namespace.
		// It can also optionally filter deployments by series name.
		// NOTE: Experimental
		List(ctx context.Context, options DeploymentListOptions) (DeploymentListIterator, error)

		// GetReachability returns reachability information for a deployment. This operation is
		// expensive, and results may be cached. Use the returned [DeploymentReachabilityInfo.LastUpdateTime]
		// to estimate cache staleness.
		// When reachability is not required, always prefer [Describe] over [GetReachability]
		// for the most up-to-date information.
		// NOTE: Experimental
		GetReachability(ctx context.Context, options DeploymentGetReachabilityOptions) (DeploymentReachabilityInfo, error)

		// GetCurrent returns the current deployment for a given deployment series.
		// NOTE: Experimental
		GetCurrent(ctx context.Context, options DeploymentGetCurrentOptions) (DeploymentInfo, error)

		// SetCurrent changes the current deployment for a given deployment series. It can also
		// update metadata for this deployment.
		// NOTE: Experimental
		SetCurrent(ctx context.Context, options DeploymentSetCurrentOptions) (DeploymentSetCurrentResponse, error)
	}
)
