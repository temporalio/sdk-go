package converter

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/adler32"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"
)

func ExampleCodecDataConverter_compression() {
	defaultConv := GetDefaultDataConverter()
	// Create Zlib compression converter
	zlibConv := NewCodecDataConverter(
		defaultConv,
		NewZlibCodec(ZlibCodecOptions{}),
	)

	// Create payloads with both
	bigString := strings.Repeat("aabbcc", 200)
	uncompPayload, _ := defaultConv.ToPayload(bigString)
	compPayload, _ := zlibConv.ToPayload(bigString)

	// The zlib payload is smaller
	fmt.Printf("Uncompressed payload size: %v (encoding: %s)\n",
		len(uncompPayload.Data), uncompPayload.Metadata[MetadataEncoding])
	fmt.Printf("Compressed payload is smaller? %v (encoding: %s)\n",
		len(compPayload.Data) < len(uncompPayload.Data), compPayload.Metadata[MetadataEncoding])

	// Convert from payload and confirm the same string. This uses the same
	// compression converter because the converter does not do anything to
	// payloads it didn't previously convert.
	var uncompValue, compValue string
	_ = zlibConv.FromPayload(uncompPayload, &uncompValue)
	_ = zlibConv.FromPayload(compPayload, &compValue)
	fmt.Printf("Uncompressed payload back to original? %v\n", uncompValue == bigString)
	fmt.Printf("Compressed payload back to original? %v\n", compValue == bigString)

	// Output:
	// Uncompressed payload size: 1202 (encoding: json/plain)
	// Compressed payload is smaller? true (encoding: binary/zlib)
	// Uncompressed payload back to original? true
	// Compressed payload back to original? true
}

type SomeStruct struct{ MyValue string }

func TestEncodingDataConverter(t *testing.T) {
	assertEncodingDataConverter(t, "foo")
	assertEncodingDataConverter(t, nil)
	assertEncodingDataConverter(t, []byte("foo"))
	assertEncodingDataConverter(t, &SomeStruct{MyValue: "somestring"})
}

func assertEncodingDataConverter(t *testing.T, data any) {
	defaultConv := GetDefaultDataConverter()
	zlibConv := NewCodecDataConverter(
		defaultConv,
		// Always encode
		NewZlibCodec(ZlibCodecOptions{AlwaysEncode: true}),
	)

	// To/FromPayload
	compPayload, err := zlibConv.ToPayload(data)
	require.NoError(t, err)
	require.Equal(t, "binary/zlib", string(compPayload.Metadata[MetadataEncoding]))
	var newData any
	if data == nil {
		newData = &newData
	} else if data != nil {
		newData = reflect.New(reflect.TypeOf(data)).Interface()
	}
	require.NoError(t, zlibConv.FromPayload(compPayload, newData))
	if data == nil {
		require.Nil(t, newData)
	} else {
		require.Equal(t, data, reflect.ValueOf(newData).Elem().Interface())
	}

	// To/FromPayloads
	compPayloads, err := zlibConv.ToPayloads(data)
	require.NoError(t, err)
	if data == nil {
		newData = &newData
	} else if data != nil {
		newData = reflect.New(reflect.TypeOf(data)).Interface()
	}
	require.NoError(t, zlibConv.FromPayloads(compPayloads, newData))
	if data == nil {
		require.Nil(t, newData)
	} else {
		require.Equal(t, data, reflect.ValueOf(newData).Elem().Interface())
	}

	// Ignored if not known encoding
	uncompPayload, err := defaultConv.ToPayload(data)
	require.NoError(t, err)
	if data == nil {
		newData = &newData
	} else if data != nil {
		newData = reflect.New(reflect.TypeOf(data)).Interface()
	}
	require.NoError(t, zlibConv.FromPayload(uncompPayload, newData))
	if data == nil {
		require.Nil(t, newData)
	} else {
		require.Equal(t, data, reflect.ValueOf(newData).Elem().Interface())
	}

	// Check that it's ignored if too small (which all params given are)
	zlibIgnoreMinConv := NewCodecDataConverter(
		defaultConv,
		NewZlibCodec(ZlibCodecOptions{}),
	)
	require.NoError(t, err)
	compUnderMinPayload, err := zlibIgnoreMinConv.ToPayload(data)
	require.NoError(t, err)
	require.True(t, proto.Equal(uncompPayload, compUnderMinPayload))
}

func TestPayloadCodecHTTPHandler(t *testing.T) {
	defaultConv := GetDefaultDataConverter()
	codec := NewZlibCodec(ZlibCodecOptions{AlwaysEncode: true})
	handler := NewPayloadCodecHTTPHandler(codec)

	req, err := http.NewRequest("GET", "/encode", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)

	req, err = http.NewRequest("POST", "/missing", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	req, err = http.NewRequest("POST", "/encode", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)

	payloads, _ := defaultConv.ToPayloads("test")
	payloadsJSON, _ := json.Marshal(payloads)

	fmt.Printf("%s", payloadsJSON)

	req, err = http.NewRequest("POST", "/encode", bytes.NewReader(payloadsJSON))
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	encodedPayloadsJSON := strings.TrimSpace(rr.Body.String())
	require.NotEqual(t, payloadsJSON, encodedPayloadsJSON)

	req, err = http.NewRequest("POST", "/decode", strings.NewReader(encodedPayloadsJSON))
	if err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	decodedPayloadsJSON := strings.TrimSpace(rr.Body.String())
	require.Equal(t, string(payloadsJSON), decodedPayloadsJSON)
}

type testCodec struct {
	encoding   string
	encodeFrom string
}

func TestCodecDataConverterCodec(t *testing.T) {
	custom := newCapturingDC()
	parent := NewCodecDataConverter(custom, &testCodec{
		encoding: "inner", encodeFrom: "json/plain",
	})
	dc := NewCodecDataConverter(parent, &testCodec{encoding: "outer", encodeFrom: "inner"}, &signingCodec{})
	payload, err := GetDefaultDataConverter().ToPayload("header")
	require.NoError(t, err)
	for name, composed := range map[string]DataConverter{
		"direct":      dc,
		"empty outer": NewCodecDataConverter(dc),
		"empty inner": NewCodecDataConverter(NewCodecDataConverter(parent),
			&testCodec{encoding: "outer", encodeFrom: "inner"}, &signingCodec{}),
	} {
		t.Run(name, func(t *testing.T) {
			codec := composed.(PayloadCodecProvider).Codec()
			require.NotNil(t, codec)
			codec = codec.(PayloadCodecWithSerializationContext).
				WithSerializationContext(WorkflowSerializationContext{WorkflowID: "headers"})
			encoded, err := codec.Encode([]*commonpb.Payload{payload})
			require.NoError(t, err)
			require.Equal(t, "outer", string(encoded[0].Metadata[MetadataEncoding]))
			decoded, err := codec.Decode(encoded)
			require.NoError(t, err)
			require.True(t, proto.Equal(payload, decoded[0]))
		})
	}
	require.Nil(t, NewCodecDataConverter(NewCodecDataConverter(custom)).(PayloadCodecProvider).Codec())
	require.Empty(t, custom.getCapturedContexts())
}

func (e *testCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.Metadata[MetadataEncoding]) != e.encodeFrom {
			return payloads, fmt.Errorf("unexpected encoding: %s", p.Metadata[MetadataEncoding])
		}

		b, err := proto.Marshal(p)
		if err != nil {
			return payloads, err
		}

		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{MetadataEncoding: []byte(e.encoding)},
			Data:     b,
		}
	}

	return result, nil
}

func (e *testCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.Metadata[MetadataEncoding]) != e.encoding {
			return payloads, fmt.Errorf("unexpected encoding: %s", p.Metadata[MetadataEncoding])
		}

		result[i] = &commonpb.Payload{}
		err := proto.Unmarshal(p.Data, result[i])
		if err != nil {
			return payloads, err
		}
	}
	return result, nil
}

func TestRemoteDataConverter(t *testing.T) {
	defaultConv := GetDefaultDataConverter()
	codecs := []PayloadCodec{
		&testCodec{encoding: "encrypted", encodeFrom: "compressed"},
		&testCodec{encoding: "compressed", encodeFrom: "json/plain"},
	}
	handler := NewPayloadCodecHTTPHandler(codecs...)

	server := httptest.NewServer(handler)
	defer server.Close()

	localConverter := NewCodecDataConverter(
		defaultConv,
		codecs...,
	)

	remoteConverter := NewRemoteDataConverter(
		defaultConv,
		RemoteDataConverterOptions{Endpoint: server.URL},
	)

	unencodedPayloads, err := defaultConv.ToPayloads("test", "payloads")
	require.NoError(t, err)

	localEncodedPayloads, err := localConverter.ToPayloads("test", "payloads")
	require.NoError(t, err)
	remoteEncodedPayloads, err := remoteConverter.ToPayloads("test", "payloads")
	require.NoError(t, err)

	require.NotEqual(t, unencodedPayloads, localEncodedPayloads)
	require.True(t, proto.Equal(localEncodedPayloads, remoteEncodedPayloads))

	unencodedPayload, err := defaultConv.ToPayload("test")
	require.NoError(t, err)

	localEncodedPayload, err := localConverter.ToPayload("test")
	require.NoError(t, err)
	remoteEncodedPayload, err := remoteConverter.ToPayload("test")
	require.NoError(t, err)

	require.NotEqual(t, unencodedPayload, localEncodedPayload)
	require.True(t, proto.Equal(localEncodedPayload, remoteEncodedPayload))
}

func TestRemotePayloadCodecTrailingSlash(t *testing.T) {
	handler := NewPayloadCodecHTTPHandler(NewZlibCodec(ZlibCodecOptions{AlwaysEncode: true}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codec/encode" && r.URL.Path != "/codec/decode" {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()

	payload, err := GetDefaultDataConverter().ToPayload("test")
	require.NoError(t, err)

	for _, suffix := range []string{"", "/"} {
		t.Run(fmt.Sprintf("%d_trailing_slashes", len(suffix)), func(t *testing.T) {
			codec := NewRemotePayloadCodec(RemotePayloadCodecOptions{Endpoint: server.URL + "/codec" + suffix})

			encoded, err := codec.Encode([]*commonpb.Payload{payload})
			require.NoError(t, err)
			require.Len(t, encoded, 1)
			require.Equal(t, "binary/zlib", string(encoded[0].Metadata[MetadataEncoding]))

			decoded, err := codec.Decode(encoded)
			require.NoError(t, err)
			require.Len(t, decoded, 1)
			require.True(t, proto.Equal(payload, decoded[0]))
		})
	}
}

func TestRemoteDataConverterCodec(t *testing.T) {
	server := httptest.NewServer(NewPayloadCodecHTTPHandler(&testCodec{
		encoding: "remote", encodeFrom: "inner",
	}))
	defer server.Close()
	remote := NewRemoteDataConverter(
		NewCodecDataConverter(GetDefaultDataConverter(), &testCodec{
			encoding: "inner", encodeFrom: "json/plain",
		}),
		RemoteDataConverterOptions{Endpoint: server.URL},
	)
	for _, dc := range []DataConverter{
		remote,
		NewCodecDataConverter(remote, &testCodec{encoding: "outer", encodeFrom: "remote"}),
	} {
		payload, err := GetDefaultDataConverter().ToPayload("header")
		require.NoError(t, err)
		expected, err := dc.ToPayload("header")
		require.NoError(t, err)
		codec := dc.(PayloadCodecProvider).Codec()
		require.NotNil(t, codec)
		encoded, err := codec.Encode([]*commonpb.Payload{payload})
		require.NoError(t, err)
		require.Len(t, encoded, 1)
		require.True(t, proto.Equal(expected, encoded[0]))
		decoded, err := codec.Decode(encoded)
		require.NoError(t, err)
		require.Len(t, decoded, 1)
		require.True(t, proto.Equal(payload, decoded[0]))
	}
}

func TestRawValueCodec(t *testing.T) {
	require := require.New(t)
	defaultConv := GetDefaultDataConverter()
	// Create Zlib compression converter
	zlibConv := NewCodecDataConverter(
		defaultConv,
		NewZlibCodec(ZlibCodecOptions{AlwaysEncode: true}),
	)

	// To/FromPayload
	data := "test raw value"
	dataPayload, err := defaultConv.ToPayload(data)
	rawValue := NewRawValue(dataPayload)
	require.NoError(err)

	compPayload, err := zlibConv.ToPayload(rawValue)
	require.NoError(err)
	require.Equal("binary/zlib", string(compPayload.Metadata[MetadataEncoding]))
	require.False(proto.Equal(rawValue.Payload(), compPayload))

	newData := reflect.New(reflect.TypeFor[string]()).Interface()
	require.NoError(zlibConv.FromPayload(compPayload, newData))
	require.Equal(data, reflect.ValueOf(newData).Elem().Interface())

	// To/FromPayloads
	compPayloads, err := zlibConv.ToPayloads(rawValue)
	require.NoError(err)

	require.Len(compPayloads.Payloads, 1)
	require.False(proto.Equal(rawValue.Payload(), compPayloads.Payloads[0]))

	newData = reflect.New(reflect.TypeFor[string]()).Interface()
	require.NoError(zlibConv.FromPayloads(compPayloads, newData))
	require.Equal(data, reflect.ValueOf(newData).Elem().Interface())
}

func TestRawValueJsonConverter(t *testing.T) {
	data := "test raw value"
	defaultConv := GetDefaultDataConverter()
	dataPayload, err := defaultConv.ToPayload(data)
	require.NoError(t, err)
	rawValue := NewRawValue(dataPayload)

	jsonConverter := NewJSONPayloadConverter()
	_, err = jsonConverter.ToPayload(rawValue)
	require.Error(t, err)

	err = jsonConverter.FromPayload(dataPayload, &rawValue)
	require.Error(t, err)
}

// errorCodecOnEncode is a codec that always returns an error on encode.
type errorCodecOnEncode struct {
	err error
}

func (c *errorCodecOnEncode) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return nil, c.err
}

func (c *errorCodecOnEncode) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return payloads, nil
}

func TestCodecDataConverter_ToPayload_EncodeError(t *testing.T) {
	require := require.New(t)

	// Codec that always fails encoding
	errCodec := &errorCodecOnEncode{err: fmt.Errorf("some encode error")}

	// Converter with the failing codec
	conv := NewCodecDataConverter(
		GetDefaultDataConverter(),
		errCodec,
	)

	// Try to convert, should fail.
	originalPayload, err := GetDefaultDataConverter().ToPayload("foo")
	require.NoError(err)

	payload, err := conv.ToPayload("foo")
	require.Error(err)
	require.EqualError(err, "some encode error")
	// Also assert that the original payload is returned on error.
	require.True(proto.Equal(originalPayload, payload))
}

func zlibTestPayloads() []*commonpb.Payload {
	return []*commonpb.Payload{
		{},
		{Metadata: map[string][]byte{MetadataEncoding: []byte("json/plain")}, Data: []byte(`"small"`)},
		{Data: bytes.Repeat([]byte("compressible "), 10_000)},
		{Data: bytes.Repeat([]byte{0, 1, 2, 3, 4, 5, 6, 7}, 1)},
	}
}

func TestZlibCodecReuseMatchesFreshWriter(t *testing.T) {
	for _, alwaysEncode := range []bool{true, false} {
		codec := NewZlibCodec(ZlibCodecOptions{AlwaysEncode: alwaysEncode})
		// Repeat so pooled writers and readers are reused across payload sizes.
		for range 3 {
			for _, p := range zlibTestPayloads() {
				b, err := proto.Marshal(p)
				require.NoError(t, err)
				var expected bytes.Buffer
				w := zlib.NewWriter(&expected)
				_, err = w.Write(b)
				require.NoError(t, err)
				require.NoError(t, w.Close())

				encoded, err := codec.Encode([]*commonpb.Payload{p})
				require.NoError(t, err)
				if expected.Len() < len(b) || alwaysEncode {
					require.Equal(t, "binary/zlib", string(encoded[0].Metadata[MetadataEncoding]))
					require.Equal(t, expected.Bytes(), encoded[0].Data)
				} else {
					require.Same(t, p, encoded[0])
				}

				decoded, err := codec.Decode(encoded)
				require.NoError(t, err)
				require.True(t, proto.Equal(p, decoded[0]))
			}
		}
	}
}

func TestZlibCodecDecodeErrorDoesNotAffectLaterDecodes(t *testing.T) {
	codec := NewZlibCodec(ZlibCodecOptions{AlwaysEncode: true})
	payload := &commonpb.Payload{Data: bytes.Repeat([]byte("compressible "), 1_000)}
	encoded, err := codec.Encode([]*commonpb.Payload{payload})
	require.NoError(t, err)
	data := encoded[0].Data

	corrupt := bytes.Clone(data)
	corrupt[len(corrupt)-1] ^= 0xff
	for _, bad := range [][]byte{nil, []byte("not zlib"), data[:len(data)/2], corrupt} {
		in := []*commonpb.Payload{{Metadata: map[string][]byte{MetadataEncoding: []byte("binary/zlib")}, Data: bad}}
		out, err := codec.Decode(in)
		require.Error(t, err)
		require.Equal(t, in, out)

		decoded, err := codec.Decode(encoded)
		require.NoError(t, err)
		require.True(t, proto.Equal(payload, decoded[0]))
	}
}

func TestZlibCodecConcurrentUse(t *testing.T) {
	codec := NewZlibCodec(ZlibCodecOptions{AlwaysEncode: true})
	payloads := zlibTestPayloads()
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 100 {
				p := payloads[i%len(payloads)]
				encoded, err := codec.Encode([]*commonpb.Payload{p})
				if !assert.NoError(t, err) {
					return
				}
				decoded, err := codec.Decode(encoded)
				if !assert.NoError(t, err) || !assert.True(t, proto.Equal(p, decoded[0])) {
					return
				}
			}
		}()
	}
	wg.Wait()
}

func BenchmarkZlibCodecEncode(b *testing.B) {
	codec := NewZlibCodec(ZlibCodecOptions{AlwaysEncode: true})
	payloads := []*commonpb.Payload{{Data: []byte(`{"id":"abc","amount":"12.34"}`)}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := codec.Encode(payloads); err != nil {
			b.Fatal(err)
		}
	}
}

func TestZlibCodecDecodeCannotReadPreviousPayload(t *testing.T) {
	codec := NewZlibCodec(ZlibCodecOptions{AlwaysEncode: true})
	secret := &commonpb.Payload{Data: []byte("previous payload secret 0123456789 previous payload secret")}
	encoded, err := codec.Encode([]*commonpb.Payload{secret})
	require.NoError(t, err)
	secretBytes, err := proto.Marshal(secret)
	require.NoError(t, err)

	// A zlib stream whose deflate data back-references bytes from before its
	// own start, i.e. from whatever history a reused reader might still hold.
	var deflated bytes.Buffer
	fw, err := flate.NewWriterDict(&deflated, flate.BestCompression, secretBytes)
	require.NoError(t, err)
	_, err = fw.Write(secretBytes)
	require.NoError(t, err)
	require.NoError(t, fw.Close())
	crafted := append([]byte{0x78, 0x9c}, deflated.Bytes()...)
	crafted = binary.BigEndian.AppendUint32(crafted, adler32.Checksum(secretBytes))
	attack := []*commonpb.Payload{{Metadata: map[string][]byte{MetadataEncoding: []byte("binary/zlib")}, Data: crafted}}
	_, err = NewZlibCodec(ZlibCodecOptions{}).Decode(attack)
	require.Error(t, err, "crafted stream must depend on prior history")

	for range 3 {
		decoded, err := codec.Decode(encoded)
		require.NoError(t, err)
		require.True(t, proto.Equal(secret, decoded[0]))

		_, err = codec.Decode(attack)
		require.Error(t, err)
	}
}
