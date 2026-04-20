package nexusclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strconv"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
)

// NewCompletionHTTPRequest creates an HTTP request that delivers an operation completion to a given URL.
func NewCompletionHTTPRequest(ctx context.Context, url string, completion OperationCompletion) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return nil, err
	}
	if err := completion.applyToHTTPRequest(httpReq); err != nil {
		return nil, err
	}

	httpReq.Header.Set(headerUserAgent, userAgent)
	return httpReq, nil
}

// OperationCompletion is input for [NewCompletionHTTPRequest].
// It has two implementations: [OperationCompletionSuccessful] and [OperationCompletionUnsuccessful].
type OperationCompletion interface {
	applyToHTTPRequest(*http.Request) error
}

// OperationCompletionSuccessful is input for [NewCompletionHTTPRequest], used to deliver successful operation results.
type OperationCompletionSuccessful struct {
	// Header to send in the completion request.
	// Note that this is a Nexus header, not an HTTP header.
	Header nexus.Header

	// A [Reader] that may be directly set on the completion or constructed when instantiating via
	// [NewOperationCompletionSuccessful].
	// Automatically closed when the completion is delivered.
	Reader *nexus.Reader
	// OperationToken is the unique token for this operation. Used when a completion callback is received before a
	// started response.
	OperationToken string
	// StartTime is the time the operation started. Used when a completion callback is received before a started response.
	StartTime time.Time
	// CloseTime is the time the operation completed. Used when a completion callback is received before a started response.
	CloseTime time.Time
	// Links are used to link back to the operation when a completion callback is received before a started response.
	Links []nexus.Link
}

// OperationCompletionSuccessfulOptions are options for [NewOperationCompletionSuccessful].
type OperationCompletionSuccessfulOptions struct {
	// Optional serializer for the result. Defaults to the SDK's default Serializer, which handles JSONables, byte
	// slices and nils.
	Serializer nexus.Serializer
	// OperationToken is the unique token for this operation. Used when a completion callback is received before a
	// started response.
	OperationToken string
	// StartTime is the time the operation started. Used when a completion callback is received before a started response.
	StartTime time.Time
	// CloseTime is the time the operation completed. Used when a completion callback is received before a started response.
	CloseTime time.Time
	// Links are used to link back to the operation when a completion callback is received before a started response.
	Links []nexus.Link
}

// NewOperationCompletionSuccessful constructs an [OperationCompletionSuccessful] from a given result.
func NewOperationCompletionSuccessful(result any, options OperationCompletionSuccessfulOptions) (*OperationCompletionSuccessful, error) {
	reader, ok := result.(*nexus.Reader)
	if !ok {
		content, ok := result.(*nexus.Content)
		if !ok {
			serializer := options.Serializer
			if serializer == nil {
				serializer = nexus.DefaultSerializer()
			}
			var err error
			content, err = serializer.Serialize(result)
			if err != nil {
				return nil, err
			}
		}
		header := maps.Clone(content.Header)
		if header == nil {
			header = make(nexus.Header, 1)
		}
		header["length"] = strconv.Itoa(len(content.Data))

		reader = &nexus.Reader{
			Header:     header,
			ReadCloser: io.NopCloser(bytes.NewReader(content.Data)),
		}
	}

	return &OperationCompletionSuccessful{
		Header:         make(nexus.Header),
		Reader:         reader,
		OperationToken: options.OperationToken,
		StartTime:      options.StartTime,
		CloseTime:      options.CloseTime,
		Links:          options.Links,
	}, nil
}

func (c *OperationCompletionSuccessful) applyToHTTPRequest(request *http.Request) error {
	if request.Header == nil {
		request.Header = make(http.Header, len(c.Header)+len(c.Reader.Header)+1) // +1 for headerOperationState
	}
	if c.Reader.Header != nil {
		addContentHeaderToHTTPHeader(c.Reader.Header, request.Header)
	}
	if c.Header != nil {
		addNexusHeaderToHTTPHeader(c.Header, request.Header)
	}
	request.Header.Set(headerOperationState, string(nexus.OperationStateSucceeded))

	if c.Header.Get(nexus.HeaderOperationToken) == "" && c.OperationToken != "" {
		request.Header.Set(nexus.HeaderOperationToken, c.OperationToken)
	}
	if c.Header.Get(headerOperationStartTime) == "" && !c.StartTime.IsZero() {
		request.Header.Set(headerOperationStartTime, c.StartTime.Format(http.TimeFormat))
	}
	if c.Header.Get(headerLink) == "" {
		if err := addLinksToHTTPHeader(c.Links, request.Header); err != nil {
			return err
		}
	}

	request.Body = c.Reader.ReadCloser
	return nil
}

// OperationCompletionUnsuccessful is input for [NewCompletionHTTPRequest], used to deliver unsuccessful operation
// results.
type OperationCompletionUnsuccessful struct {
	// Header to send in the completion request.
	// Note that this is a Nexus header, not an HTTP header.
	Header nexus.Header
	// State of the operation, should be failed or canceled.
	State nexus.OperationState
	// OperationToken is the unique token for this operation. Used when a completion callback is received before a
	// started response.
	OperationToken string
	// StartTime is the time the operation started. Used when a completion callback is received before a started response.
	StartTime time.Time
	// CloseTime is the time the operation completed. This may be different from the time the completion callback is delivered.
	CloseTime time.Time
	// Links are used to link back to the operation when a completion callback is received before a started response.
	Links []nexus.Link
	// Failure object to send with the completion.
	Failure nexus.Failure
}

// OperationCompletionUnsuccessfulOptions are options for [NewOperationCompletionUnsuccessful].
type OperationCompletionUnsuccessfulOptions struct {
	// Convert a [Failure] instance to and from an [error]. Defaults to
	// [failureErrorFailureConverter].
	FailureConverter failureConverter
	// OperationID is the unique ID for this operation. Used when a completion callback is received before a started response.
	//
	// Deprecated: Use OperatonToken instead.
	OperationID string
	// OperationToken is the unique token for this operation. Used when a completion callback is received before a
	// started response.
	OperationToken string
	// StartTime is the time the operation started. Used when a completion callback is received before a started response.
	StartTime time.Time
	// CloseTime is the time the operation completed. This may be different from the time the completion callback is delivered.
	CloseTime time.Time
	// Links are used to link back to the operation when a completion callback is received before a started response.
	Links []nexus.Link
}

// NewOperationCompletionUnsuccessful constructs an [OperationCompletionUnsuccessful] from a given error.
func NewOperationCompletionUnsuccessful(opErr *nexus.OperationError, options OperationCompletionUnsuccessfulOptions) (*OperationCompletionUnsuccessful, error) {
	if options.FailureConverter == nil {
		options.FailureConverter = &failureErrorFailureConverter{}
	}

	return &OperationCompletionUnsuccessful{
		Header:         make(nexus.Header),
		State:          opErr.State,
		Failure:        options.FailureConverter.ErrorToFailure(opErr.Cause),
		OperationToken: options.OperationToken,
		StartTime:      options.StartTime,
		CloseTime:      options.CloseTime,
		Links:          options.Links,
	}, nil
}

func (c *OperationCompletionUnsuccessful) applyToHTTPRequest(request *http.Request) error {
	if request.Header == nil {
		request.Header = make(http.Header, len(c.Header)+2) // +2 for headerOperationState and content-type
	}
	if c.Header != nil {
		addNexusHeaderToHTTPHeader(c.Header, request.Header)
	}
	request.Header.Set(headerOperationState, string(c.State))
	request.Header.Set("Content-Type", "application/json")

	if c.Header.Get(nexus.HeaderOperationToken) == "" && c.OperationToken != "" {
		request.Header.Set(nexus.HeaderOperationToken, c.OperationToken)
	}
	if c.Header.Get(headerOperationStartTime) == "" && !c.StartTime.IsZero() {
		request.Header.Set(headerOperationStartTime, c.StartTime.Format(http.TimeFormat))
	}
	if c.Header.Get(headerLink) == "" {
		if err := addLinksToHTTPHeader(c.Links, request.Header); err != nil {
			return err
		}
	}

	b, err := json.Marshal(c.Failure)
	if err != nil {
		return err
	}

	request.Body = io.NopCloser(bytes.NewReader(b))
	return nil
}

// CompletionRequest is input for CompletionHandler.CompleteOperation.
type CompletionRequest struct {
	// The original HTTP request.
	HTTPRequest *http.Request
	// State of the operation.
	State nexus.OperationState
	// OperationToken is the unique token for this operation. Used when a completion callback is received before a
	// started response.
	OperationToken string
	// StartTime is the time the operation started. Used when a completion callback is received before a started response.
	StartTime time.Time
	// CloseTime is the time the operation completed. This may be different from the time the completion callback is delivered.
	CloseTime time.Time
	// Links are used to link back to the operation when a completion callback is received before a started response.
	Links []nexus.Link
	// Parsed from request and set if State is failed or canceled.
	Error error
	// Extracted from request and set if State is succeeded.
	Result *nexus.LazyValue
}
