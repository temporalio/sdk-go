package converter

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// PayloadCodec is an codec that encodes or decodes the given payloads.
//
// For example, NewZlibCodec returns a PayloadCodec that can be used for
// compression.
// These can be used (and even chained) in NewCodecDataConverter.
//
// PayloadCodec methods may run while processing a Workflow Task or in a Client
// or Activity context. When called during Workflow Task processing, returning
// an error may fail the Workflow Execution, depending on the operation. A panic
// is handled according to the Worker's WorkflowPanicPolicy. With the default
// BlockWorkflow policy, a panic fails the current Workflow Task so that the
// Temporal Service can retry it. With the FailWorkflow policy, a panic instead
// fails the Workflow Execution.
//
// Codec implementations should handle transient failures locally when
// possible. Panicking outside Workflow Task processing does not request a
// Workflow Task retry.
type PayloadCodec interface {
	// Encode optionally encodes the given payloads which are guaranteed to never
	// be nil. The parameters must not be mutated.
	Encode([]*commonpb.Payload) ([]*commonpb.Payload, error)

	// Decode optionally decodes the given payloads which are guaranteed to never
	// be nil. The parameters must not be mutated.
	//
	// For compatibility reasons, implementers should take care not to decode
	// payloads that were not previously encoded.
	Decode([]*commonpb.Payload) ([]*commonpb.Payload, error)
}

// ZlibCodecOptions are options for NewZlibCodec. All fields are optional.
type ZlibCodecOptions struct {
	// If true, the zlib codec will encode the contents even if there is no size
	// benefit. Otherwise, the zlib codec will only use the encoded value if it
	// is smaller.
	AlwaysEncode bool
}

type zlibCodec struct{ options ZlibCodecOptions }

// NewZlibCodec creates a PayloadCodec for use in NewCodecDataConverter
// to support zlib payload compression.
//
// While this serves as a reasonable example of a compression encoder, callers
// may prefer alternative compression algorithms for lots of small payloads.
func NewZlibCodec(options ZlibCodecOptions) PayloadCodec { return &zlibCodec{options} }

func (z *zlibCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		// Marshal and write
		b, err := proto.Marshal(p)
		if err != nil {
			return payloads, err
		}
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		_, err = w.Write(b)
		if closeErr := w.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			return payloads, err
		}
		// Only set if smaller than original amount or has option to always encode
		if buf.Len() < len(b) || z.options.AlwaysEncode {
			result[i] = &commonpb.Payload{
				Metadata: map[string][]byte{MetadataEncoding: []byte("binary/zlib")},
				Data:     buf.Bytes(),
			}
		} else {
			result[i] = p
		}
	}
	return result, nil
}

func (*zlibCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		// Only if it's our encoding
		if string(p.Metadata[MetadataEncoding]) != "binary/zlib" {
			result[i] = p
			continue
		}
		r, err := zlib.NewReader(bytes.NewReader(p.Data))
		if err != nil {
			return payloads, err
		}
		// Read all and unmarshal
		b, err := io.ReadAll(r)
		if closeErr := r.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			return payloads, err
		}
		result[i] = &commonpb.Payload{}
		err = proto.Unmarshal(b, result[i])
		if err != nil {
			return payloads, err
		}
	}
	return result, nil
}

func decodePayloads(payloads []*commonpb.Payload, codecs []PayloadCodec) ([]*commonpb.Payload, error) {
	var err error
	// Iterate forwards decoding
	for _, codec := range codecs {
		if payloads, err = codec.Decode(payloads); err != nil {
			return payloads, err
		}
	}
	return payloads, nil
}

func encodePayloads(payloads []*commonpb.Payload, codecs []PayloadCodec) ([]*commonpb.Payload, error) {
	var err error
	// Iterate backwards encoding
	for i := len(codecs) - 1; i >= 0; i-- {
		if payloads, err = codecs[i].Encode(payloads); err != nil {
			return payloads, err
		}
	}
	return payloads, nil
}

// CodecDataConverter is a DataConverter that wraps an underlying data
// converter and supports chained encoding of just the payload without regard
// for serialization to/from actual types.
type CodecDataConverter struct {
	parent DataConverter
	codecs []PayloadCodec
}

// NewCodecDataConverter wraps the given parent DataConverter and performs
// encoding/decoding on the payload via the given codecs. When encoding for
// ToPayload(s), the codecs are applied last to first meaning the earlier
// encoders wrap the later ones. When decoding for FromPayload(s) and
// ToString(s), the decoders are applied first to last to reverse the effect.
func NewCodecDataConverter(parent DataConverter, codecs ...PayloadCodec) DataConverter {
	return &CodecDataConverter{parent, codecs}
}

func (e *CodecDataConverter) encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return encodePayloads(payloads, e.codecs)
}

func (e *CodecDataConverter) decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return decodePayloads(payloads, e.codecs)
}

// ToPayload implements DataConverter.ToPayload performing encoding on the
// result of the parent's ToPayload call.
func (e *CodecDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	payload, err := e.parent.ToPayload(value)
	if payload == nil || err != nil {
		return payload, err
	}

	encodedPayloads, err := e.encode([]*commonpb.Payload{payload})
	if err != nil {
		return payload, err
	}
	if len(encodedPayloads) != 1 {
		return payload, fmt.Errorf("received %d payloads from codec, expected 1", len(encodedPayloads))
	}
	return encodedPayloads[0], err
}

// ToPayloads implements DataConverter.ToPayloads performing encoding on the
// result of the parent's ToPayloads call.
func (e *CodecDataConverter) ToPayloads(value ...any) (*commonpb.Payloads, error) {
	payloads, err := e.parent.ToPayloads(value...)
	if payloads == nil || err != nil {
		return payloads, err
	}
	encodedPayloads, err := e.encode(payloads.Payloads)
	return &commonpb.Payloads{Payloads: encodedPayloads}, err
}

// FromPayload implements DataConverter.FromPayload performing decoding on the
// given payload before sending to the parent FromPayload.
func (e *CodecDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	if payload == nil {
		return nil
	}
	decodedPayloads, err := e.decode([]*commonpb.Payload{payload})
	if err != nil {
		return err
	}
	if len(decodedPayloads) != 1 {
		return fmt.Errorf("received %d payloads from codec, expected 1", len(decodedPayloads))
	}
	return e.parent.FromPayload(decodedPayloads[0], valuePtr)
}

// FromPayloads implements DataConverter.FromPayloads performing decoding on the
// given payloads before sending to the parent FromPayloads.
func (e *CodecDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
	if payloads == nil {
		return e.parent.FromPayloads(payloads, valuePtrs...)
	}
	decodedPayloads, err := e.decode(payloads.Payloads)
	if err != nil {
		return err
	}
	return e.parent.FromPayloads(&commonpb.Payloads{Payloads: decodedPayloads}, valuePtrs...)
}

// ToString implements DataConverter.ToString performing decoding on the given
// payload before sending to the parent ToString.
func (e *CodecDataConverter) ToString(payload *commonpb.Payload) string {
	decodedPayloads, err := e.decode([]*commonpb.Payload{payload})
	if err != nil {
		return err.Error()
	}
	if len(decodedPayloads) != 1 {
		return fmt.Errorf("received %d payloads from codec, expected 1", len(decodedPayloads)).Error()
	}
	return e.parent.ToString(decodedPayloads[0])
}

// ToStrings implements DataConverter.ToStrings using ToString for each value.
func (e *CodecDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	if payloads == nil {
		return nil
	}
	strs := make([]string, len(payloads.Payloads))
	// Perform decoding one by one here so that we return individual errors
	for i, payload := range payloads.Payloads {
		strs[i] = e.ToString(payload)
	}
	return strs
}

func (e *CodecDataConverter) WithSerializationContext(ctx SerializationContext) DataConverter {
	parent := e.parent
	if p, ok := parent.(DataConverterWithSerializationContext); ok {
		parent = p.WithSerializationContext(ctx)
	}
	codecs := make([]PayloadCodec, len(e.codecs))
	changed := parent != e.parent
	for i, c := range e.codecs {
		if cc, ok := c.(PayloadCodecWithSerializationContext); ok {
			codecs[i] = cc.WithSerializationContext(ctx)
			changed = changed || codecs[i] != c
		} else {
			codecs[i] = c
		}
	}
	if !changed {
		return e
	}
	return &CodecDataConverter{parent, codecs}
}

const remotePayloadCodecEncodePath = "/encode"
const remotePayloadCodecDecodePath = "/decode"

type codecHTTPHandler struct {
	codecs []PayloadCodec
}

func (e *codecHTTPHandler) encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return encodePayloads(payloads, e.codecs)
}

func (e *codecHTTPHandler) decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return decodePayloads(payloads, e.codecs)
}

// ServeHTTP implements the http.Handler interface.
func (e *codecHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.NotFound(w, r)
		return
	}

	path := r.URL.Path

	if !strings.HasSuffix(path, remotePayloadCodecEncodePath) &&
		!strings.HasSuffix(path, remotePayloadCodecDecodePath) {
		http.NotFound(w, r)
		return
	}

	var payloadspb commonpb.Payloads
	var err error

	if r.Body == nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	bs, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err = protojson.Unmarshal(bs, &payloadspb); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	payloads := payloadspb.Payloads

	switch {
	case strings.HasSuffix(path, remotePayloadCodecEncodePath):
		if payloads, err = e.encode(payloads); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	case strings.HasSuffix(path, remotePayloadCodecDecodePath):
		if payloads, err = e.decode(payloads); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(commonpb.Payloads{Payloads: payloads})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

// NewPayloadCodecHTTPHandler creates a http.Handler for a PayloadCodec.
// This can be used to provide a remote data converter.
func NewPayloadCodecHTTPHandler(e ...PayloadCodec) http.Handler {
	return &codecHTTPHandler{codecs: e}
}

// RemotePayloadCodecRetryOptions contains retry configuration for RemotePayloadCodec.
// Retries are disabled by default (MaximumAttempts <= 1).
//
// NOTE: When running within a Workflow Task, retries consume the Workflow Task timeout budget.
type RemotePayloadCodecRetryOptions struct {
	// InitialInterval is the initial backoff interval between retry attempts.
	// Default is 100ms when MaximumAttempts > 1 and InitialInterval is 0.
	InitialInterval time.Duration
	// BackoffCoefficient is the multiplier for exponential backoff intervals.
	// Default is 2.0 when BackoffCoefficient < 1.0.
	BackoffCoefficient float64
	// MaximumInterval is the maximum backoff interval between retry attempts.
	// Default is 5s when MaximumInterval is 0.
	MaximumInterval time.Duration
	// ExpirationInterval is the maximum duration for all retry attempts combined.
	// Optional (0 means no expiration interval).
	ExpirationInterval time.Duration
	// MaximumAttempts is the maximum number of attempts (including the initial attempt).
	// If MaximumAttempts <= 1, no retries are performed.
	MaximumAttempts int
	// IsRetryable determines if a given error (or HTTP status failure) should be retried.
	// If nil, transient network errors, HTTP 429, and HTTP 5xx responses are retried.
	IsRetryable func(err error) bool
}

// HTTPStatusError represents an error returned when the remote payload codec responds with a non-200 HTTP status code.
type HTTPStatusError struct {
	StatusCode int
	Status     string
	Message    string
}

func (e *HTTPStatusError) Error() string {
	if e.Status != "" {
		return fmt.Sprintf("%s: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("%s: %s", http.StatusText(e.StatusCode), e.Message)
}

func defaultIsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests ||
			httpErr.StatusCode == http.StatusInternalServerError ||
			httpErr.StatusCode == http.StatusBadGateway ||
			httpErr.StatusCode == http.StatusServiceUnavailable ||
			httpErr.StatusCode == http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

// RemotePayloadCodecOptions are options for RemotePayloadCodec.
// Client is optional.
type RemotePayloadCodecOptions struct {
	Endpoint      string
	ModifyRequest func(*http.Request) error
	Client        http.Client
	RetryOptions  RemotePayloadCodecRetryOptions
}

type remotePayloadCodec struct {
	options RemotePayloadCodecOptions
}

// NewRemotePayloadCodec creates a PayloadCodec using the remote endpoint configured by RemotePayloadCodecOptions.
func NewRemotePayloadCodec(options RemotePayloadCodecOptions) PayloadCodec {
	return &remotePayloadCodec{options}
}

// Encode uses the remote payload codec endpoint to encode payloads.
func (pc *remotePayloadCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return pc.encodeOrDecode(pc.options.Endpoint+remotePayloadCodecEncodePath, payloads)
}

// Decode uses the remote payload codec endpoint to decode payloads.
func (pc *remotePayloadCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return pc.encodeOrDecode(pc.options.Endpoint+remotePayloadCodecDecodePath, payloads)
}

func (pc *remotePayloadCodec) encodeOrDecode(endpoint string, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	requestPayloads, err := json.Marshal(commonpb.Payloads{Payloads: payloads})
	if err != nil {
		return payloads, fmt.Errorf("unable to marshal payloads: %w", err)
	}

	retryOpts := pc.options.RetryOptions
	maxAttempts := retryOpts.MaximumAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	initialInterval := retryOpts.InitialInterval
	if initialInterval <= 0 {
		initialInterval = 100 * time.Millisecond
	}
	backoffCoefficient := retryOpts.BackoffCoefficient
	if backoffCoefficient < 1.0 {
		backoffCoefficient = 2.0
	}
	maxInterval := retryOpts.MaximumInterval
	if maxInterval <= 0 {
		maxInterval = 5 * time.Second
	}

	isRetryable := retryOpts.IsRetryable
	if isRetryable == nil {
		isRetryable = defaultIsRetryable
	}

	var startTime time.Time
	if retryOpts.ExpirationInterval > 0 {
		startTime = time.Now()
	}

	currentInterval := initialInterval
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestPayloads))
		if err != nil {
			return payloads, fmt.Errorf("unable to build request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		if pc.options.ModifyRequest != nil {
			err = pc.options.ModifyRequest(req)
			if err != nil {
				return payloads, err
			}
		}

		response, err := pc.options.Client.Do(req)
		if err == nil {
			if response.StatusCode == http.StatusOK {
				defer func() { _ = response.Body.Close() }()
				bs, err := io.ReadAll(response.Body)
				if err != nil {
					return payloads, fmt.Errorf("failed to read response body: %w", err)
				}
				var resultPayloads commonpb.Payloads
				err = protojson.Unmarshal(bs, &resultPayloads)
				if err != nil {
					return payloads, fmt.Errorf("unable to unmarshal payloads: %w", err)
				}
				if len(payloads) != len(resultPayloads.Payloads) {
					return payloads, fmt.Errorf("received %d payloads from remote codec, expected %d", len(resultPayloads.Payloads), len(payloads))
				}
				return resultPayloads.Payloads, nil
			}

			message, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			err = &HTTPStatusError{
				StatusCode: response.StatusCode,
				Status:     http.StatusText(response.StatusCode),
				Message:    string(message),
			}
		}

		lastErr = err

		if attempt >= maxAttempts || !isRetryable(err) {
			return payloads, err
		}

		if retryOpts.ExpirationInterval > 0 && time.Since(startTime)+currentInterval > retryOpts.ExpirationInterval {
			return payloads, err
		}

		time.Sleep(currentInterval)

		nextInterval := time.Duration(float64(currentInterval) * backoffCoefficient)
		if nextInterval > maxInterval {
			nextInterval = maxInterval
		}
		currentInterval = nextInterval
	}

	return payloads, lastErr
}

// Fields Endpoint, ModifyRequest, Client of RemotePayloadCodecOptions are also
// exposed here in RemoteDataConverterOptions for backwards compatibility.

// RemoteDataConverterOptions are options for NewRemoteDataConverter.
type RemoteDataConverterOptions struct {
	Endpoint      string
	ModifyRequest func(*http.Request) error
	Client        http.Client
}

type remoteDataConverter struct {
	parent       DataConverter
	payloadCodec PayloadCodec
}

// NewRemoteDataConverter wraps the given parent DataConverter and performs
// encoding/decoding on the payload via the remote endpoint.
func NewRemoteDataConverter(parent DataConverter, options RemoteDataConverterOptions) DataConverter {
	options.Endpoint = strings.TrimSuffix(options.Endpoint, "/")
	payloadCodec := NewRemotePayloadCodec(RemotePayloadCodecOptions{
		Endpoint:      options.Endpoint,
		ModifyRequest: options.ModifyRequest,
		Client:        options.Client,
	})
	return &remoteDataConverter{parent, payloadCodec}
}

// ToPayload implements DataConverter.ToPayload performing remote encoding on the
// result of the parent's ToPayload call.
func (rdc *remoteDataConverter) ToPayload(value any) (*commonpb.Payload, error) {
	payload, err := rdc.parent.ToPayload(value)
	if payload == nil || err != nil {
		return payload, err
	}
	encodedPayloads, err := rdc.payloadCodec.Encode([]*commonpb.Payload{payload})
	if err != nil {
		return payload, err
	}
	return encodedPayloads[0], err
}

// ToPayloads implements DataConverter.ToPayloads performing remote encoding on the
// result of the parent's ToPayloads call.
func (rdc *remoteDataConverter) ToPayloads(value ...any) (*commonpb.Payloads, error) {
	payloads, err := rdc.parent.ToPayloads(value...)
	if payloads == nil || err != nil {
		return payloads, err
	}
	encodedPayloads, err := rdc.payloadCodec.Encode(payloads.Payloads)
	return &commonpb.Payloads{Payloads: encodedPayloads}, err
}

// FromPayload implements DataConverter.FromPayload performing remote decoding on the
// given payload before sending to the parent FromPayload.
func (rdc *remoteDataConverter) FromPayload(payload *commonpb.Payload, valuePtr any) error {
	decodedPayloads, err := rdc.payloadCodec.Decode([]*commonpb.Payload{payload})
	if err != nil {
		return err
	}
	return rdc.parent.FromPayload(decodedPayloads[0], valuePtr)
}

// FromPayloads implements DataConverter.FromPayloads performing remote decoding on the
// given payloads before sending to the parent FromPayloads.
func (rdc *remoteDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...any) error {
	if payloads == nil {
		return rdc.parent.FromPayloads(payloads, valuePtrs...)
	}

	decodedPayloads, err := rdc.payloadCodec.Decode(payloads.Payloads)
	if err != nil {
		return err
	}
	return rdc.parent.FromPayloads(&commonpb.Payloads{Payloads: decodedPayloads}, valuePtrs...)
}

// ToString implements DataConverter.ToString performing remote decoding on the given
// payload before sending to the parent ToString.
func (rdc *remoteDataConverter) ToString(payload *commonpb.Payload) string {
	if payload == nil {
		return rdc.parent.ToString(payload)
	}

	decodedPayloads, err := rdc.payloadCodec.Decode([]*commonpb.Payload{payload})
	if err != nil {
		return err.Error()
	}
	return rdc.parent.ToString(decodedPayloads[0])
}

// ToStrings implements DataConverter.ToStrings using ToString for each value.
func (rdc *remoteDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	if payloads == nil {
		return nil
	}

	strs := make([]string, len(payloads.Payloads))
	// Perform decoding one by one here so that we return individual errors
	for i, payload := range payloads.Payloads {
		strs[i] = rdc.ToString(payload)
	}
	return strs
}
