package converter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

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
	fmt.Printf("Compressed payload size: %v (encoding: %s)\n",
		len(compPayload.Data), compPayload.Metadata[MetadataEncoding])

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
	// Compressed payload size: 57 (encoding: binary/zlib)
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

func assertEncodingDataConverter(t *testing.T, data interface{}) {
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
	var newData interface{}
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

	newData := reflect.New(reflect.TypeOf(data)).Interface()
	require.NoError(zlibConv.FromPayload(compPayload, newData))
	require.Equal(data, reflect.ValueOf(newData).Elem().Interface())

	// To/FromPayloads
	compPayloads, err := zlibConv.ToPayloads(rawValue)
	require.NoError(err)

	require.Len(compPayloads.Payloads, 1)
	require.False(proto.Equal(rawValue.Payload(), compPayloads.Payloads[0]))

	newData = reflect.New(reflect.TypeOf(data)).Interface()
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
