package secretsmanager

import (
	"context"
	"errors"
	"net"

	"github.com/aws/smithy-go"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
)

// telemetry is where the cache reports to, taken from the options of the client being configured.
type telemetry struct {
	// May be nil, in which case nothing is logged
	logger  log.Logger
	metrics client.MetricsHandler
}

// Metric/label names, must be the same across SDKs
const (
	metricFetchFailed  = "temporal_aws_secrets_manager_fetch_failed"
	metricFetchLatency = "temporal_aws_secrets_manager_fetch_latency"
	metricCacheLookup  = "temporal_aws_secrets_manager_cache_lookup"

	labelSecretType    = "secret_type"
	labelFetchPath     = "fetch_path"
	labelFailureReason = "failure_reason"
	labelFallback      = "fallback"
	labelOutcome       = "outcome"
	labelResult        = "result"
)

// secret_type label values
// Note: a bundled client cert/key secret is reported as secretTypeClientCert
type secretType string

const (
	secretTypeAPIKey       secretType = "api_key"
	secretTypeClientCert   secretType = "tls_client_cert"
	secretTypeClientKey    secretType = "tls_client_key"
	secretTypeServerCACert secretType = "tls_server_ca_cert"
)

// fallback label values
type fallback string

const (
	fallbackStale fallback = "stale"
	fallbackNone  fallback = "none"
)

// outcome label values
type fetchOutcome string

const (
	outcomeSuccess fetchOutcome = "success"
	outcomeFailure fetchOutcome = "failure"
)

// result label values
type lookupResult string

const (
	lookupHit              lookupResult = "hit"
	lookupMiss             lookupResult = "miss"
	lookupMissWithFallback lookupResult = "miss_with_fallback"
)

// fetch_path label values
type fetchPath string

const (
	// AWS Parameters and Secrets Lambda Extension
	fetchPathExtension fetchPath = "extension"
	// Direct fetch from AWS Secrets Manager via AWS SDK
	fetchPathSDK fetchPath = "sdk"
)

// failure_reason label values
type failureReason string

const (
	failureResourceNotFound     failureReason = "resource_not_found"
	failureAccessDenied         failureReason = "access_denied"
	failureDecryptionFailure    failureReason = "decryption_failure"
	failureInvalidRequest       failureReason = "invalid_request"
	failureThrottling           failureReason = "throttling"
	failureInternalServiceError failureReason = "internal_service_error"
	failureTimeout              failureReason = "timeout"
	failureInvalidResponse      failureReason = "invalid_response"
	// The Lambda extension doesn't return the underlying AWS error, only an HTTP status with a generic message, so its
	// errors are only classified by status class
	failureClientError failureReason = "client_error"
	failureServerError failureReason = "server_error"
	failureOther       failureReason = "other"
)

// AWS error codes mapped to failure reasons. Other codes are failureOther.
var awsErrorCodeFailureReasons = map[string]failureReason{
	"ResourceNotFoundException": failureResourceNotFound,
	"AccessDeniedException":     failureAccessDenied,
	"DecryptionFailure":         failureDecryptionFailure,
	"InvalidRequestException":   failureInvalidRequest,
	"InvalidParameterException": failureInvalidRequest,
	"ThrottlingException":       failureThrottling,
	"InternalServiceError":      failureInternalServiceError,
}

// classifyFailure returns the failure_reason metric label for a fetch error.
func classifyFailure(err error) failureReason {
	var apiErr smithy.APIError
	var extErr *lambdaExtensionStatusError
	var invalidErr *invalidResponseError
	var netErr net.Error
	switch {
	case errors.As(err, &apiErr):
		if reason, ok := awsErrorCodeFailureReasons[apiErr.ErrorCode()]; ok {
			return reason
		}
	case errors.As(err, &extErr):
		if extErr.statusCode >= 400 && extErr.statusCode < 500 {
			return failureClientError
		} else if extErr.statusCode >= 500 && extErr.statusCode < 600 {
			return failureServerError
		}
	case errors.As(err, &invalidErr):
		return failureInvalidResponse
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled),
		errors.As(err, &netErr) && netErr.Timeout():
		return failureTimeout
	}
	return failureOther
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}
