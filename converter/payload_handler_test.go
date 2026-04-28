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
	"go.temporal.io/sdk/internal/extstore"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// appendCodec — encodes unconditionally by appending a suffix to the encoding
// field and a marker byte to the data, so tests can assert exactly which codecs
// ran on which payloads by inspecting the encoding string.
// ---------------------------------------------------------------------------

type appendCodec struct {
	encodingSuffix string
	marker         byte
}

func (c *appendCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		meta := make(map[string][]byte, len(p.GetMetadata()))
		for k, v := range p.GetMetadata() {
			meta[k] = v
		}
		meta[converter.MetadataEncoding] = []byte(string(p.GetMetadata()[converter.MetadataEncoding]) + c.encodingSuffix)
		result[i] = &commonpb.Payload{
			Metadata: meta,
			Data:     append(append([]byte(nil), p.GetData()...), c.marker),
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
		meta := make(map[string][]byte, len(p.GetMetadata()))
		for k, v := range p.GetMetadata() {
			meta[k] = v
		}
		meta[converter.MetadataEncoding] = []byte(strings.TrimSuffix(enc, c.encodingSuffix))
		result[i] = &commonpb.Payload{
			Metadata: meta,
			Data:     data[:len(data)-1],
		}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// memDriver — in-memory StorageDriver.
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

// storageRefJSON mirrors the internal storageReference struct so tests can
// construct storage-reference payloads without depending on unexported types.
type storageRefJSON struct {
	DriverName  string                       `json:"driver_name"`
	DriverClaim converter.StorageDriverClaim `json:"driver_claim"`
}

// makeStorageRef creates a storage reference payload matching the internal wire format.
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

func makePayload(t *testing.T, value string) *commonpb.Payload {
	t.Helper()
	p, err := converter.GetDefaultDataConverter().ToPayload(value)
	require.NoError(t, err)
	return p
}

func createRequest(t *testing.T, payloads ...*commonpb.Payload) *bytes.Reader {
	t.Helper()
	body, err := protojson.Marshal(&commonpb.Payloads{Payloads: payloads})
	require.NoError(t, err)
	return bytes.NewReader(body)
}

func servePost(t *testing.T, handler http.Handler, path string, body *bytes.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, path, body)
	require.NoError(t, err)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func getPayloads(t *testing.T, rr *httptest.ResponseRecorder) []*commonpb.Payload {
	t.Helper()
	require.Equal(t, http.StatusOK, rr.Code, "response body: %s", rr.Body.String())
	var result commonpb.Payloads
	require.NoError(t, protojson.Unmarshal(rr.Body.Bytes(), &result))
	return result.Payloads
}

func encoding(p *commonpb.Payload) string {
	return string(p.GetMetadata()[converter.MetadataEncoding])
}

// fixedDriverSelector always routes to the provided driver.
type fixedDriverSelector struct{ driver converter.StorageDriver }

func (s fixedDriverSelector) SelectDriver(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
	return s.driver, nil
}

// ---------------------------------------------------------------------------
// Routing
// ---------------------------------------------------------------------------

func TestRouting_WrongMethod(t *testing.T) {
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodGet, "/decode", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestRouting_WrongPath(t *testing.T) {
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	rr := servePost(t, h, "/missing", bytes.NewReader(nil))
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestRouting_NilBody(t *testing.T) {
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodPost, "/decode", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRouting_MountedAtPrefix(t *testing.T) {
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	p := makePayload(t, "hello")
	rr := servePost(t, h, "/myapp/codec/decode", createRequest(t, p))
	require.Equal(t, http.StatusOK, rr.Code)
}

// ---------------------------------------------------------------------------
// /decode
// ---------------------------------------------------------------------------

func TestDecode_AppliesPostThenPreCodecs(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PreStorageCodecs:  []converter.PayloadCodec{preCodec},
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	p := makePayload(t, "hello")
	originalEncoding := encoding(p)
	preEncoded, err := preCodec.Encode([]*commonpb.Payload{p})
	require.NoError(t, err)
	postEncoded, err := postCodec.Encode(preEncoded)
	require.NoError(t, err)

	rr := servePost(t, h, "/decode", createRequest(t, postEncoded[0]))
	result := getPayloads(t, rr)
	require.Len(t, result, 1)
	require.Equal(t, originalEncoding, encoding(result[0]))
}

func TestDecode_NoDrivers_StorageRefFails(t *testing.T) {
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	ref := makeStorageRef(t, "drv", "k1")
	rr := servePost(t, h, "/decode", createRequest(t, ref))
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "no storage driver is configured")
}

func TestDecode_NoDrivers_MixedBatch_StorageRefFails(t *testing.T) {
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	regular := makePayload(t, "data")
	ref := makeStorageRef(t, "drv", "k1")
	rr := servePost(t, h, "/decode", createRequest(t, regular, ref))
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "no storage driver is configured")
}

func TestDecode_RoundTrip(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PreStorageCodecs:  []converter.PayloadCodec{preCodec},
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	originals := []*commonpb.Payload{
		makePayload(t, "first"),
		makePayload(t, "second"),
	}

	preEncoded, err := preCodec.Encode(originals)
	require.NoError(t, err)
	postEncoded, err := postCodec.Encode(preEncoded)
	require.NoError(t, err)

	rr := servePost(t, h, "/decode", createRequest(t, postEncoded...))
	decoded := getPayloads(t, rr)
	require.Len(t, decoded, 2)

	for i := range originals {
		require.True(t, proto.Equal(originals[i], decoded[i]),
			"payload %d: encode→decode should be identity", i)
	}
}

// ---------------------------------------------------------------------------
// /decode?preserveStorageRefs=true
// ---------------------------------------------------------------------------

func TestDecode_PreserveStorageRefs_AllRefs(t *testing.T) {
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	ref1 := makeStorageRef(t, "drv", "k1")
	ref2 := makeStorageRef(t, "drv", "k2")
	postWrapped, err := postCodec.Encode([]*commonpb.Payload{ref1, ref2})
	require.NoError(t, err)

	rr := servePost(t, h, "/decode?preserveStorageRefs=true", createRequest(t, postWrapped...))
	result := getPayloads(t, rr)
	require.Len(t, result, 2)
	require.True(t, extstore.IsStorageReference(result[0]))
	require.True(t, extstore.IsStorageReference(result[1]))
}

func TestDecode_PreserveStorageRefs_NoRefs(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PreStorageCodecs:  []converter.PayloadCodec{preCodec},
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	p := makePayload(t, "hello")
	originalEncoding := encoding(p)
	preEncoded, err := preCodec.Encode([]*commonpb.Payload{p})
	require.NoError(t, err)
	postEncoded, err := postCodec.Encode(preEncoded)
	require.NoError(t, err)

	rr := servePost(t, h, "/decode?preserveStorageRefs=true", createRequest(t, postEncoded[0]))
	result := getPayloads(t, rr)
	require.Len(t, result, 1)
	require.Equal(t, originalEncoding, encoding(result[0]))
}

func TestDecode_PreserveStorageRefs_MixedBatch(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PreStorageCodecs:  []converter.PayloadCodec{preCodec},
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	// Interleaved [ref, regular, ref, regular] to exercise ordering preservation.
	ref1 := makeStorageRef(t, "drv", "k1")
	ref2 := makeStorageRef(t, "drv", "k2")
	regular1 := makePayload(t, "first")
	regular2 := makePayload(t, "second")
	originalEncoding1 := encoding(regular1)
	originalEncoding2 := encoding(regular2)

	preEncoded, err := preCodec.Encode([]*commonpb.Payload{regular1, regular2})
	require.NoError(t, err)
	postEncoded, err := postCodec.Encode([]*commonpb.Payload{ref1, preEncoded[0], ref2, preEncoded[1]})
	require.NoError(t, err)

	rr := servePost(t, h, "/decode?preserveStorageRefs=true", createRequest(t, postEncoded...))
	result := getPayloads(t, rr)
	require.Len(t, result, 4)
	require.True(t, extstore.IsStorageReference(result[0]))
	require.Equal(t, originalEncoding1, encoding(result[1]))
	require.True(t, extstore.IsStorageReference(result[2]))
	require.Equal(t, originalEncoding2, encoding(result[3]))
}

func TestDecode_PreserveStorageRefs_CaseInsensitive(t *testing.T) {
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	ref := makeStorageRef(t, "drv", "k1")
	postWrapped, err := postCodec.Encode([]*commonpb.Payload{ref})
	require.NoError(t, err)

	for _, val := range []string{"TRUE", "True", "tRuE"} {
		t.Run(val, func(t *testing.T) {
			rr := servePost(t, h, "/decode?preserveStorageRefs="+val, createRequest(t, postWrapped[0]))
			result := getPayloads(t, rr)
			require.Len(t, result, 1)
			require.True(t, extstore.IsStorageReference(result[0]))
		})
	}
}

// TestDecode_PreserveStorageRefs_False verifies that preserveStorageRefs=false
// behaves the same as omitting the parameter — retrieval is performed.
func TestDecode_PreserveStorageRefs_False(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	driver := newMemDriver("drv")

	stored := makePayload(t, "stored-value")
	preEncoded, err := preCodec.Encode([]*commonpb.Payload{stored})
	require.NoError(t, err)
	driver.data["k1"] = proto.Clone(preEncoded[0]).(*commonpb.Payload)

	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PreStorageCodecs: []converter.PayloadCodec{preCodec},
		ExternalStorage:  converter.ExternalStorage{Drivers: []converter.StorageDriver{driver}},
	})
	require.NoError(t, err)

	ref := makeStorageRef(t, "drv", "k1")
	rr := servePost(t, h, "/decode?preserveStorageRefs=false", createRequest(t, ref))
	result := getPayloads(t, rr)
	require.Len(t, result, 1)
	require.True(t, proto.Equal(stored, result[0]))
}

// ---------------------------------------------------------------------------
// /download
// ---------------------------------------------------------------------------

func TestDownload_SingleRef(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	driver := newMemDriver("testdrv")

	storedPayload := makePayload(t, "stored-value")
	preEncoded, err := preCodec.Encode([]*commonpb.Payload{storedPayload})
	require.NoError(t, err)
	driver.data["my-key"] = proto.Clone(preEncoded[0]).(*commonpb.Payload)

	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PreStorageCodecs: []converter.PayloadCodec{preCodec},
		ExternalStorage:  converter.ExternalStorage{Drivers: []converter.StorageDriver{driver}},
	})
	require.NoError(t, err)

	ref := makeStorageRef(t, "testdrv", "my-key")
	rr := servePost(t, h, "/download", createRequest(t, ref))
	result := getPayloads(t, rr)
	require.Len(t, result, 1)
	require.True(t, proto.Equal(storedPayload, result[0]),
		"expected %v, got %v", storedPayload, result[0])
}

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

	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		ExternalStorage: converter.ExternalStorage{Drivers: []converter.StorageDriver{driver}},
	})
	require.NoError(t, err)

	refs := make([]*commonpb.Payload, len(payloads))
	for i := range payloads {
		refs[i] = makeStorageRef(t, "drv", fmt.Sprintf("k%d", i))
	}

	rr := servePost(t, h, "/download", createRequest(t, refs...))
	result := getPayloads(t, rr)
	require.Len(t, result, 3)
	for i, p := range payloads {
		require.True(t, proto.Equal(p, result[i]), "payload %d mismatch", i)
	}
}

func TestDownload_NonStorageRef(t *testing.T) {
	driver := newMemDriver("drv")
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		ExternalStorage: converter.ExternalStorage{Drivers: []converter.StorageDriver{driver}},
	})
	require.NoError(t, err)

	regular := makePayload(t, "not-a-ref")
	rr := servePost(t, h, "/download", createRequest(t, regular))
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestDownload_NoDrivers(t *testing.T) {
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	ref := makeStorageRef(t, "drv", "k1")
	rr := servePost(t, h, "/download", createRequest(t, ref))
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "no storage driver is configured")
}

func TestDownload_UnknownDriver(t *testing.T) {
	driver := newMemDriver("registered")
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		ExternalStorage: converter.ExternalStorage{Drivers: []converter.StorageDriver{driver}},
	})
	require.NoError(t, err)

	ref := makeStorageRef(t, "unregistered", "k1")
	rr := servePost(t, h, "/download", createRequest(t, ref))
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestDownload_MultipleDrivers(t *testing.T) {
	driverA := newMemDriver("driver-a")
	driverB := newMemDriver("driver-b")

	driverA.data["key-a"] = makePayload(t, "from-a")
	driverB.data["key-b"] = makePayload(t, "from-b")

	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		ExternalStorage: converter.ExternalStorage{
			Drivers:        []converter.StorageDriver{driverA, driverB},
			DriverSelector: fixedDriverSelector{driver: driverA},
		},
	})
	require.NoError(t, err)

	refs := []*commonpb.Payload{
		makeStorageRef(t, "driver-a", "key-a"),
		makeStorageRef(t, "driver-b", "key-b"),
	}
	rr := servePost(t, h, "/download", createRequest(t, refs...))
	result := getPayloads(t, rr)
	require.Len(t, result, 2)

	var got0, got1 string
	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(result[0], &got0))
	require.Equal(t, "from-a", got0)

	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(result[1], &got1))
	require.Equal(t, "from-b", got1)
}

// ---------------------------------------------------------------------------
// /encode
// ---------------------------------------------------------------------------

func TestEncode_NoCodecsNoStorage(t *testing.T) {
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{})
	require.NoError(t, err)

	p := makePayload(t, "hello")
	rr := servePost(t, h, "/encode", createRequest(t, p))
	result := getPayloads(t, rr)
	require.Len(t, result, 1)
	require.True(t, proto.Equal(p, result[0]))
}

func TestEncode_PreStorageCodecsOnly(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PreStorageCodecs: []converter.PayloadCodec{preCodec},
	})
	require.NoError(t, err)

	p := makePayload(t, "hello")
	rr := servePost(t, h, "/encode", createRequest(t, p))
	result := getPayloads(t, rr)
	require.Len(t, result, 1)
	require.True(t, strings.HasSuffix(encoding(result[0]), ".pre"))
}

func TestEncode_PostStorageCodecsOnly(t *testing.T) {
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	p := makePayload(t, "hello")
	rr := servePost(t, h, "/encode", createRequest(t, p))
	result := getPayloads(t, rr)
	require.Len(t, result, 1)
	require.True(t, strings.HasSuffix(encoding(result[0]), ".post"))
}

func TestEncode_PreAndPostCodecs(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PreStorageCodecs:  []converter.PayloadCodec{preCodec},
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
	})
	require.NoError(t, err)

	p := makePayload(t, "hello")
	rr := servePost(t, h, "/encode", createRequest(t, p))
	result := getPayloads(t, rr)
	require.Len(t, result, 1)
	// Pre runs first, post runs second — ".pre.post" is the expected final suffix.
	require.True(t, strings.HasSuffix(encoding(result[0]), ".pre.post"))
}

func TestEncode_WithExternalStorage(t *testing.T) {
	driver := newMemDriver("drv")
	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		ExternalStorage: converter.ExternalStorage{
			Drivers:              []converter.StorageDriver{driver},
			PayloadSizeThreshold: 1, // store everything
		},
	})
	require.NoError(t, err)

	p := makePayload(t, "hello")
	rr := servePost(t, h, "/encode", createRequest(t, p))
	result := getPayloads(t, rr)
	require.Len(t, result, 1)
	require.True(t, extstore.IsStorageReference(result[0]))
	require.Len(t, driver.data, 1)
}

func TestEncode_DecodeRoundTrip(t *testing.T) {
	preCodec := &appendCodec{encodingSuffix: ".pre", marker: 'P'}
	postCodec := &appendCodec{encodingSuffix: ".post", marker: 'O'}
	driver := newMemDriver("drv")

	h, err := converter.NewPayloadHTTPHandler(converter.PayloadHTTPHandlerOptions{
		PreStorageCodecs:  []converter.PayloadCodec{preCodec},
		PostStorageCodecs: []converter.PayloadCodec{postCodec},
		ExternalStorage: converter.ExternalStorage{
			Drivers:              []converter.StorageDriver{driver},
			PayloadSizeThreshold: 1,
		},
	})
	require.NoError(t, err)

	originals := []*commonpb.Payload{
		makePayload(t, "first"),
		makePayload(t, "second"),
	}

	encRR := servePost(t, h, "/encode", createRequest(t, originals...))
	encoded := getPayloads(t, encRR)

	decRR := servePost(t, h, "/decode", createRequest(t, encoded...))
	decoded := getPayloads(t, decRR)

	require.Len(t, decoded, len(originals))
	for i, orig := range originals {
		require.True(t, proto.Equal(orig, decoded[i]), "payload %d: encode→decode should be identity", i)
	}
}
