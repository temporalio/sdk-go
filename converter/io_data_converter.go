package converter

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"io/ioutil"

	"github.com/gogo/protobuf/proto"
	commonpb "go.temporal.io/api/common/v1"
)

// IODataConverterOptions are options for NewIODataConverter.
type IODataConverterOptions struct {

	// Algorithm is the algorithm for raw IO conversion. All fields must be set.
	// ZlibAlgorithm can be used for compression.
	Algorithm IODataConverterAlgorithm

	// MinSize is the size the payload must reach in bytes before the converter is
	// applied when converting to a payload. If the payload in bytes is smaller
	// than this amount, it is not converted, just the underlying payload is
	// returned from ToPayload(s).
	MinSize int
}

// IODataConverterAlgorithm represents an IO algorithm to perform byte
// conversion. It is most commonly used for features like compression. See
// ZlibAlgorithm for an example.
type IODataConverterAlgorithm struct {

	// Encoding is the unique encoding for this algorithm. This is set on
	// ToPayload(s) and only applies this converter if it is the same value set
	// when converting in FromPayload(s). This cannot be empty.
	Encoding string

	// NewReader instantiates a reader that converts data from the underlying
	// reader. If the resulting reader is also an io.Closer, it is closed by the
	// converter. This cannot be nil.
	NewReader func(io.Reader) (io.Reader, error)

	// NewWriter instantiates a writer that converts data to the underlying
	// writer. If the resulting writer is also an io.Closer, it is closed by the
	// converter before the underlying writer's bytes are used. This cannot be
	// nil.
	NewWriter func(io.Writer) (io.Writer, error)
}

// ZlibAlgorithm is an algorithm for zlib compression.
var ZlibAlgorithm = IODataConverterAlgorithm{
	Encoding:  "binary/zlib",
	NewReader: func(r io.Reader) (io.Reader, error) { return zlib.NewReader(r) },
	NewWriter: func(w io.Writer) (io.Writer, error) { return zlib.NewWriter(w), nil },
}

// IODataConverter is a DataConverter that wraps another converter and performs
// reader/writer IO on the results of the other converter's
// FromPayload(s)/ToPayload(s) operations respectively.
type IODataConverter struct {
	underlying DataConverter
	options    IODataConverterOptions
}

// NewIODataConverter creates a new IODataConverter wrapping the given
// underlying data converter.
func NewIODataConverter(underlying DataConverter, options IODataConverterOptions) (*IODataConverter, error) {
	if underlying == nil {
		return nil, fmt.Errorf("underlying converter must be provided")
	} else if options.Algorithm.Encoding == "" {
		return nil, fmt.Errorf("missing algorithm encoding")
	} else if options.Algorithm.NewReader == nil {
		return nil, fmt.Errorf("missing algorithm NewReader")
	} else if options.Algorithm.NewWriter == nil {
		return nil, fmt.Errorf("missing algorithm NewWriter")
	}
	return &IODataConverter{underlying: underlying, options: options}, nil
}

// ToPayload implements DataConverter.ToPayload using the configured algorithm's
// writer on the result of the underlying ToPayload result.
func (i *IODataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	payload, err := i.underlying.ToPayload(value)
	if payload == nil || err != nil {
		return payload, err
	}
	return i.toPayload(payload)
}

// ToPayloads implements DataConverter.ToPayloads using the configured
// algorithm's writer on the result of the underlying ToPayloads result.
func (i *IODataConverter) ToPayloads(value ...interface{}) (*commonpb.Payloads, error) {
	payloads, err := i.underlying.ToPayloads(value...)
	if payloads == nil || err != nil {
		return payloads, err
	}
	newPayloads := &commonpb.Payloads{Payloads: make([]*commonpb.Payload, len(payloads.Payloads))}
	for idx, payload := range payloads.Payloads {
		if newPayloads.Payloads[idx], err = i.toPayload(payload); err != nil {
			return nil, err
		}
	}
	return newPayloads, nil
}

func (i *IODataConverter) toPayload(payload *commonpb.Payload) (*commonpb.Payload, error) {
	// If there is a min size and we're under it, do nothing
	if payload == nil || (i.options.MinSize > 0 && payload.Size() < i.options.MinSize) {
		return payload, nil
	}
	// Marshal and convert
	b, err := proto.Marshal(payload)
	if err != nil {
		return nil, err
	}
	b, err = i.write(b)
	if err != nil {
		return nil, err
	}
	return &commonpb.Payload{
		Metadata: map[string][]byte{MetadataEncoding: []byte(i.options.Algorithm.Encoding)},
		Data:     b,
	}, nil
}

func (i *IODataConverter) write(b []byte) ([]byte, error) {
	// Create writer for use
	var buf bytes.Buffer
	w, err := i.options.Algorithm.NewWriter(&buf)
	if err != nil {
		return nil, err
	}
	// Write the bytes
	_, err = w.Write(b)
	// Close no matter what. We choose to close here instead of deferred because
	// many IO algorithms want the writer closed to flush all writes.
	if closer, _ := w.(io.Closer); closer != nil {
		// Only set the error if not already set
		if closeErr := closer.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return buf.Bytes(), err
}

// FromPayload implements DataConverter.FromPayload using the configured
// algorithm's reader before sending to the underlying FromPayload.
func (i *IODataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	payload, err := i.fromPayload(payload)
	if err != nil {
		return err
	}
	return i.underlying.FromPayload(payload, valuePtr)
}

// FromPayloads implements DataConverter.FromPayloads using the configured
// algorithm's reader before sending to the underlying FromPayloads.
func (i *IODataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	if payloads == nil {
		return i.underlying.FromPayloads(payloads, valuePtrs...)
	}
	newPayloads := &commonpb.Payloads{Payloads: make([]*commonpb.Payload, len(payloads.Payloads))}
	for idx, payload := range payloads.Payloads {
		var err error
		if newPayloads.Payloads[idx], err = i.fromPayload(payload); err != nil {
			return err
		}
	}
	return i.underlying.FromPayloads(newPayloads, valuePtrs...)
}

func (i *IODataConverter) fromPayload(payload *commonpb.Payload) (*commonpb.Payload, error) {
	// If the payload is not the expected encoding, do not convert it
	if payload == nil || string(payload.Metadata[MetadataEncoding]) != i.options.Algorithm.Encoding {
		return payload, nil
	}
	// Convert and unmarshal
	b, err := i.read(payload.Data)
	if err != nil {
		return nil, err
	}
	var newPayload commonpb.Payload
	if err := proto.Unmarshal(b, &newPayload); err != nil {
		return nil, err
	}
	return &newPayload, nil
}

func (i *IODataConverter) read(b []byte) ([]byte, error) {
	r, err := i.options.Algorithm.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	// Read it all
	b, err = ioutil.ReadAll(r)
	// Close reader
	if closer, _ := r.(io.Closer); closer != nil {
		// Only set the error if not already set
		if closeErr := closer.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return b, err
}

// ToString implements DataConverter.ToString using the configured algorithm's
// reader before sending to the underlying ToString.
func (i *IODataConverter) ToString(input *commonpb.Payload) string {
	input, err := i.fromPayload(input)
	if err != nil {
		return err.Error()
	}
	return i.underlying.ToString(input)
}

// ToStrings implements DataConverter.ToStrings using ToString for each value.
func (i *IODataConverter) ToStrings(input *commonpb.Payloads) []string {
	if input == nil {
		return nil
	}
	strs := make([]string, len(input.Payloads))
	for idx, payload := range input.Payloads {
		strs[idx] = i.ToString(payload)
	}
	return strs
}
