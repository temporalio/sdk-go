package converter_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// appendCodec — test codec that appends a marker byte and a suffix to the
// encoding field on encode, and reverses both on decode. It operates on ALL
// payloads regardless of their current encoding, which lets us verify exactly
// which codecs were applied to which payloads by inspecting the encoding chain.
// ---------------------------------------------------------------------------

type appendCodec struct {
	encodingSuffix string
	marker         byte
}

func (c *appendCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		enc := string(p.GetMetadata()[converter.MetadataEncoding]) + c.encodingSuffix
		data := append(append([]byte(nil), p.GetData()...), c.marker)
		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{converter.MetadataEncoding: []byte(enc)},
			Data:     data,
		}
	}
	return result, nil
}

func (c *appendCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		enc := string(p.GetMetadata()[converter.MetadataEncoding])
		if !strings.HasSuffix(enc, c.encodingSuffix) {
			return nil, fmt.Errorf("appendCodec.Decode: encoding %q does not have expected suffix %q", enc, c.encodingSuffix)
		}
		data := p.GetData()
		if len(data) == 0 || data[len(data)-1] != c.marker {
			return nil, fmt.Errorf("appendCodec.Decode: expected trailing marker byte %d", c.marker)
		}
		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{converter.MetadataEncoding: []byte(strings.TrimSuffix(enc, c.encodingSuffix))},
			Data:     data[:len(data)-1],
		}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// memDriver — minimal in-memory StorageDriver for testing /ui/download.
// ---------------------------------------------------------------------------

type memDriver struct {
	name string
	data map[string]*commonpb.Payload
}

func newMemDriver(name string) *memDriver {
	return &memDriver{name: name, data: map[string]*commonpb.Payload{}}
}

func (d *memDriver) Name() string { return d.name }
func (d *memDriver) Type() string { return "mem" }

func (d *memDriver) Store(_ converter.StorageDriverStoreContext, payloads []*commonpb.Payload) ([]converter.StorageDriverClaim, error) {
	claims := make([]converter.StorageDriverClaim, len(payloads))
	for i, p := range payloads {
		key := fmt.Sprintf("key-%d-%d", i, len(d.data))
		d.data[key] = proto.Clone(p).(*commonpb.Payload)
		claims[i] = converter.StorageDriverClaim{ClaimData: map[string]string{"key": key}}
	}
	return claims, nil
}

func (d *memDriver) Retrieve(_ converter.StorageDriverRetrieveContext, claims []converter.StorageDriverClaim) ([]*commonpb.Payload, error) {
	payloads := make([]*commonpb.Payload, len(claims))
	for i, c := range claims {
		p, ok := d.data[c.ClaimData["key"]]
		if !ok {
			return nil, fmt.Errorf("memDriver: key not found: %q", c.ClaimData["key"])
		}
		payloads[i] = proto.Clone(p).(*commonpb.Payload)
	}
	return payloads, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// storageRefJSON mirrors the internal storageReference struct for constructing
// test storage-reference payloads without depending on unexported types.
type storageRefJSON struct {
	DriverName  string                       `json:"driver_name"`
	DriverClaim converter.StorageDriverClaim `json:"driver_claim"`
}

// makeStorageRef creates a storage reference payload pointing to the given
// driver and claim key. The payload format matches the internal implementation.
func makeStorageRef(t *testing.T, driverName, key string) *commonpb.Payload {
	t.Helper()
	data, err := json.Marshal(storageRefJSON{
		DriverName:  driverName,
		DriverClaim: converter.StorageDriverClaim{ClaimData: map[string]string{"key": key}},
	})
	require.NoError(t, err)
	return &commonpb.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding: []byte("json/external-storage-reference"),
		},
		Data: data,
	}
}

// makePayload creates a simple json/plain payload from a string value.
func makePayload(t *testing.T, value string) *commonpb.Payload {
	t.Helper()
	p, err := converter.GetDefaultDataConverter().ToPayload(value)
	require.NoError(t, err)
	return p
}

// encodeRequest JSON-encodes a Payloads proto and returns a request body reader.
func encodeRequest(t *testing.T, payloads ...*commonpb.Payload) *bytes.Reader {
	t.Helper()
	body, err := protojson.Marshal(&commonpb.Payloads{Payloads: payloads})
	require.NoError(t, err)
	return bytes.NewReader(body)
}

// servePost sends a POST to path on handler and returns the recorder.
func servePost(t *testing.T, handler http.Handler, path string, body *bytes.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, path, body)
	require.NoError(t, err)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// decodeResponse parses the handler's JSON response into a []*commonpb.Payload slice.
func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder) []*commonpb.Payload {
	t.Helper()
	require.Equal(t, http.StatusOK, rr.Code, "response body: %s", rr.Body.String())
	var result commonpb.Payloads
	require.NoError(t, protojson.Unmarshal(rr.Body.Bytes(), &result))
	return result.Payloads
}

// encoding returns the MetadataEncoding value of a payload as a string.
func encoding(p *commonpb.Payload) string {
	return string(p.GetMetadata()[converter.MetadataEncoding])
}

// ---------------------------------------------------------------------------
// Routing
// ---------------------------------------------------------------------------

func TestRouting_WrongMethod(t *testing.T) {
	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodGet, "/ui/decode", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestRouting_WrongPath(t *testing.T) {
	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	rr := servePost(t, h, "/missing", bytes.NewReader(nil))
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestRouting_NilBody(t *testing.T) {
	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodPost, "/ui/decode", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRouting_MountedAtPrefix(t *testing.T) {
	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	p := makePayload(t, "hello")
	rr := servePost(t, h, "/myapp/codec/ui/decode", encodeRequest(t, p))
	require.Equal(t, http.StatusOK, rr.Code)
}

// ---------------------------------------------------------------------------
// /ui/decode
// ---------------------------------------------------------------------------

// TestDecode_AppliesPostThenPreCodecs verifies that post-storage codecs run
// first (first-to-last) and pre-storage codecs run second on non-refs.
func TestDecode_AppliesPostThenPreCodecs(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{
		PreStorageCodecs:  []converter.PayloadCodec{preCodec},
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	// Build a payload that has been through the full encode chain (pre then post).
	p := makePayload(t, "hello")
	originalEncoding := encoding(p)
	preEncoded, err := preCodec.Encode([]*commonpb.Payload{p})
	require.NoError(t, err)
	postEncoded, err := postCodec.Encode(preEncoded)
	require.NoError(t, err)

	// Decode through the handler and verify the result matches the original.
	rr := servePost(t, h, "/ui/decode", encodeRequest(t, postEncoded[0]))
	result := decodeResponse(t, rr)
	require.Len(t, result, 1)
	require.Equal(t, originalEncoding, encoding(result[0]))
}

// TestDecode_StorageRefReturnedAsIs verifies that a payload which is a storage
// reference after post-storage decoding is returned without applying pre-storage
// codecs (the caller should resolve it via /ui/download).
func TestDecode_StorageRefReturnedAsIs(t *testing.T) {
	// Pre-storage codec that would fail if applied to a storage ref
	// (its Decode checks for the ".pre" suffix).
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{
		PreStorageCodecs:  []converter.PayloadCodec{preCodec},
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	// Simulate a post-encoded storage reference (post codec was applied to a
	// storage ref before sending it to this handler).
	ref := makeStorageRef(t, "drv", "k1")
	postWrapped, err := postCodec.Encode([]*commonpb.Payload{ref})
	require.NoError(t, err)

	rr := servePost(t, h, "/ui/decode", encodeRequest(t, postWrapped[0]))
	result := decodeResponse(t, rr)
	require.Len(t, result, 1)

	// After post-decode we should see the original storage reference, not an error.
	require.Equal(t, "json/external-storage-reference", encoding(result[0]))
}

// TestDecode_MixedBatch verifies that in a batch containing both a regular
// payload and a storage reference, the regular payload is fully decoded while
// the storage reference is left as-is.
func TestDecode_MixedBatch(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{
		PreStorageCodecs:  []converter.PayloadCodec{preCodec},
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	// Regular payload encoded through the full chain (pre then post).
	regular := makePayload(t, "data")
	originalEncoding := encoding(regular)
	preEncoded, err := preCodec.Encode([]*commonpb.Payload{regular})
	require.NoError(t, err)
	encoded, err := postCodec.Encode(preEncoded)
	require.NoError(t, err)

	// Storage reference wrapped by the post codec only.
	ref := makeStorageRef(t, "drv", "k1")
	postWrappedRef, err := postCodec.Encode([]*commonpb.Payload{ref})
	require.NoError(t, err)

	rr := servePost(t, h, "/ui/decode", encodeRequest(t, encoded[0], postWrappedRef[0]))
	result := decodeResponse(t, rr)
	require.Len(t, result, 2)

	// Regular payload should be fully decoded.
	require.Equal(t, originalEncoding, encoding(result[0]))
	// Storage reference should be left as the raw reference.
	require.Equal(t, "json/external-storage-reference", encoding(result[1]))
}

// TestDecode_RoundTrip verifies that a payload encoded through the full codec
// chain (pre then post) is restored to its original form after /ui/decode.
func TestDecode_RoundTrip(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{
		PreStorageCodecs:  []converter.PayloadCodec{preCodec},
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	originals := []*commonpb.Payload{
		makePayload(t, "first"),
		makePayload(t, "second"),
	}

	// Encode through the full chain directly (pre then post).
	preEncoded, err := preCodec.Encode(originals)
	require.NoError(t, err)
	encoded, err := postCodec.Encode(preEncoded)
	require.NoError(t, err)

	rr := servePost(t, h, "/ui/decode", encodeRequest(t, encoded...))
	decoded := decodeResponse(t, rr)
	require.Len(t, decoded, 2)

	for i := range originals {
		require.True(t, proto.Equal(originals[i], decoded[i]),
			"payload %d: encode→decode should be identity", i)
	}
}

// ---------------------------------------------------------------------------
// /ui/download
// ---------------------------------------------------------------------------

// TestDownload_HappyPath verifies that a storage reference is retrieved from
// the configured driver and the result is decoded through pre-storage codecs.
func TestDownload_HappyPath(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	driver := newMemDriver("testdrv")

	// Pre-populate the driver with a pre-storage-encoded payload.
	storedPayload := makePayload(t, "stored-value")
	preEncoded, err := preCodec.Encode([]*commonpb.Payload{storedPayload})
	require.NoError(t, err)
	driver.data["my-key"] = proto.Clone(preEncoded[0]).(*commonpb.Payload)

	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{
		PreStorageCodecs: []converter.PayloadCodec{preCodec},
		StorageDrivers:   []converter.StorageDriver{driver},
	})
	require.NoError(t, err)

	ref := makeStorageRef(t, "testdrv", "my-key")
	rr := servePost(t, h, "/ui/download", encodeRequest(t, ref))
	result := decodeResponse(t, rr)
	require.Len(t, result, 1)

	// The retrieved payload should have been pre-storage decoded.
	require.True(t, proto.Equal(storedPayload, result[0]),
		"expected %v, got %v", storedPayload, result[0])
}

// TestDownload_MultipleRefs verifies fan-out retrieval of multiple storage
// references in a single /ui/download request.
func TestDownload_MultipleRefs(t *testing.T) {
	driver := newMemDriver("drv")
	payloads := []*commonpb.Payload{
		makePayload(t, "alpha"),
		makePayload(t, "beta"),
		makePayload(t, "gamma"),
	}
	for i, p := range payloads {
		key := fmt.Sprintf("k%d", i)
		driver.data[key] = proto.Clone(p).(*commonpb.Payload)
	}

	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{
		StorageDrivers: []converter.StorageDriver{driver},
	})
	require.NoError(t, err)

	refs := make([]*commonpb.Payload, len(payloads))
	for i := range payloads {
		refs[i] = makeStorageRef(t, "drv", fmt.Sprintf("k%d", i))
	}

	rr := servePost(t, h, "/ui/download", encodeRequest(t, refs...))
	result := decodeResponse(t, rr)
	require.Len(t, result, 3)
	for i, p := range payloads {
		require.True(t, proto.Equal(p, result[i]), "payload %d mismatch", i)
	}
}

// TestDownload_NonStorageRef verifies that /ui/download returns 400 when any
// payload in the request is not a storage reference.
func TestDownload_NonStorageRef(t *testing.T) {
	driver := newMemDriver("drv")
	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{
		StorageDrivers: []converter.StorageDriver{driver},
	})
	require.NoError(t, err)

	regular := makePayload(t, "not-a-ref")
	rr := servePost(t, h, "/ui/download", encodeRequest(t, regular))
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

// TestDownload_NoDrivers verifies that /ui/download returns 400 when no storage
// drivers are configured.
func TestDownload_NoDrivers(t *testing.T) {
	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	ref := makeStorageRef(t, "drv", "k1")
	rr := servePost(t, h, "/ui/download", encodeRequest(t, ref))
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

// TestDownload_UnknownDriver verifies that /ui/download returns 400 when the
// storage reference names a driver that is not registered.
func TestDownload_UnknownDriver(t *testing.T) {
	driver := newMemDriver("registered")
	h, err := converter.NewUIPayloadHTTPHandler(converter.UIPayloadHTTPHandlerOptions{
		StorageDrivers: []converter.StorageDriver{driver},
	})
	require.NoError(t, err)

	ref := makeStorageRef(t, "unregistered", "k1")
	rr := servePost(t, h, "/ui/download", encodeRequest(t, ref))
	require.Equal(t, http.StatusBadRequest, rr.Code)
}
