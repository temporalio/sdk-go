# Resource ID Fields Implementation

This document lists all the resource_id fields that were added to Temporal API request messages for multi-cell routing. The resource_id field is used by the proxy to extract routing information and create `temporal-resource-id` headers.

## Summary

Total resource_id fields added: **14**
Implementation status: **12/14 completed**
Testing status: **11/12 tested** (91.7% test coverage)

## Request Message Categories

### 1. Workflow Task Requests (2/2 completed)

| Message | Field | Expected Value | Implementation Status | Test Status |
|---------|-------|----------------|----------------------|-------------|
| `RespondWorkflowTaskCompletedRequest` | `resource_id = 18` | Workflow ID from original task | ✅ Completed | ✅ Tested |
| `RespondWorkflowTaskFailedRequest` | `resource_id = 11` | Workflow ID from original task | ✅ Completed | ✅ Tested |

### 2. Activity Task Requests (8/8 completed)

| Message | Field | Expected Value | Implementation Status | Test Status |
|---------|-------|----------------|----------------------|-------------|
| `RecordActivityTaskHeartbeatRequest` | `resource_id = 5` | Workflow ID or activity ID for standalone activities | ✅ Completed | ✅ Tested |
| `RecordActivityTaskHeartbeatByIdRequest` | `resource_id = 7` | "workflow:workflow_id" or "activity:activity_id" for standalone | ✅ Completed | ✅ Tested |
| `RespondActivityTaskCompletedRequest` | `resource_id = 8` | Workflow ID or activity ID for standalone activities | ✅ Completed | ✅ Tested |
| `RespondActivityTaskCompletedByIdRequest` | `resource_id = 7` | "workflow:workflow_id" or "activity:activity_id" for standalone | ✅ Completed | ✅ Tested |
| `RespondActivityTaskFailedRequest` | `resource_id = 9` | Workflow ID or activity ID for standalone activities | ✅ Completed | ✅ Tested |
| `RespondActivityTaskFailedByIdRequest` | `resource_id = 8` | "workflow:workflow_id" or "activity:activity_id" for standalone | ✅ Completed | ✅ Tested |
| `RespondActivityTaskCanceledRequest` | `resource_id = 8` | Workflow ID or activity ID for standalone activities | ✅ Completed | ✅ Tested |
| `RespondActivityTaskCanceledByIdRequest` | `resource_id = 8` | "workflow:workflow_id" or "activity:activity_id" for standalone | ✅ Completed | ✅ Tested |

### 3. Batch Operation Requests (1/1 completed)

| Message | Field | Expected Value | Implementation Status | Test Status |
|---------|-------|----------------|----------------------|-------------|
| `ExecuteMultiOperationRequest` | `resource_id = 3` | Should match `operations[0].start_workflow.workflow_id` | ✅ Completed | ✅ Tested |

### 4. Worker Requests (1/3 partial)

| Message | Field | Expected Value | Implementation Status | Test Status |
|---------|-------|----------------|----------------------|-------------|
| `RecordWorkerHeartbeatRequest` | `resource_id = 4` | Contains the worker grouping key | ✅ Completed | ❌ Not tested |
| `FetchWorkerConfigRequest` | `resource_id = 7` | Contains the worker grouping key | ❌ Not implemented in SDK | N/A |
| `UpdateWorkerConfigRequest` | `resource_id = 7` | Contains the worker grouping key | ❌ Not implemented in SDK | N/A |

## Resource ID Patterns

### Workflow Activities
- **Standard Activities**: Use workflow ID from the containing workflow
- **Pattern**: `task.WorkflowExecution.WorkflowId`

### Standalone Activities  
- **Activities running outside workflows**: Use activity ID
- **Pattern**: `activityId` when workflow ID is empty
- **ByID variants**: Use prefixed format like `"workflow:workflow_id"` or `"activity:activity_id"`

### Worker Operations
- **Worker heartbeats and config**: Use worker grouping key
- **Pattern**: `client.workerGroupingKey`


### Batch Operations
- **Multi-operation requests**: Use workflow ID from first operation
- **Pattern**: `startRequest.WorkflowId` from first start_workflow operation

## Helper Functions

### `getActivityResourceId(workflowId, activityId string) string`
Encapsulates the logic for choosing between workflow ID and activity ID:
```go
func getActivityResourceId(workflowId, activityId string) string {
    if workflowId != "" {
        return workflowId
    }
    return activityId
}
```

### `getActivityResourceIdFromCtx(ctx context.Context) string`
Extracts resource ID from activity context:
```go
func getActivityResourceIdFromCtx(ctx context.Context) string {
    env := getActivityEnvironmentFromCtx(ctx)
    if env == nil {
        return ""
    }
    if env.workflowExecution.ID != "" {
        return env.workflowExecution.ID
    }
    return env.activityID
}
```

## Proto Field Locations

All fields are defined in `/Users/tconley/api-go/proto/api/temporal/api/workflowservice/v1/request_response.proto`

## Testing Status

### Tested Fields (11/12)

**Workflow Task Requests (2/2):**
- ✅ `RespondWorkflowTaskCompletedRequest` - Tested with real workflow task processing
- ✅ `RespondWorkflowTaskFailedRequest` - Tested with unregistered workflow scenarios

**Activity Task Requests (8/8):**
- ✅ `RecordActivityTaskHeartbeatRequest` - Tested with workflow and standalone activities
- ✅ `RecordActivityTaskHeartbeatByIdRequest` - Tested with workflow and standalone activities via client SDK
- ✅ `RespondActivityTaskCompletedRequest` - Tested via `convertActivityResultToRespondRequest` validation
- ✅ `RespondActivityTaskCompletedByIdRequest` - Tested via `convertActivityResultToRespondRequestByID` validation
- ✅ `RespondActivityTaskFailedRequest` - Tested via `convertActivityResultToRespondRequest` validation
- ✅ `RespondActivityTaskFailedByIdRequest` - Tested via `convertActivityResultToRespondRequestByID` validation
- ✅ `RespondActivityTaskCanceledRequest` - Tested via `convertActivityResultToRespondRequest` validation
- ✅ `RespondActivityTaskCanceledByIdRequest` - Tested via `convertActivityResultToRespondRequestByID` validation

**Batch Operation Requests (1/1):**
- ✅ `ExecuteMultiOperationRequest` - Tested via UpdateWithStartWorkflow operation

### Untested Fields (1/12)

**Worker Requests (1/1):**
- ❌ `RecordWorkerHeartbeatRequest` - No tests yet

**Not implemented in SDK (2):**
- N/A `FetchWorkerConfigRequest` - Not implemented in SDK
- N/A `UpdateWorkerConfigRequest` - Not implemented in SDK

## Test Files

- `/Users/tconley/sdk-go/internal/resource_id_impl_test.go` - Main resource ID test suite
  - `TestResourceIDImplementation` - Tests workflow and activity task resource ID fields
  - Uses `convertActivityResultToRespondRequest` and `convertActivityResultToRespondRequestByID` as validation points

## Testing Strategy

Resource ID validation can be tested through:

1. **gRPC Interceptors**: Capture outgoing requests and validate resource_id fields
2. **Header Validation**: Verify `temporal-resource-id` headers are present 
3. **Integration Tests**: Test end-to-end routing behavior
4. **Unit Tests**: Validate helper function logic
5. **Conversion Function Tests**: Validate resource_id population via SDK conversion functions

## Implementation Files

Key files modified during implementation:
- `/Users/tconley/sdk-go/internal/internal_task_handlers.go`
- `/Users/tconley/sdk-go/internal/internal_task_pollers.go`
- `/Users/tconley/sdk-go/internal/internal_workflow_client.go`
- `/Users/tconley/sdk-go/internal/internal_worker_heartbeat.go`
- `/Users/tconley/sdk-go/internal/internal_nexus_task_handler.go`
- `/Users/tconley/sdk-go/internal/internal_nexus_task_poller.go`