package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

// jsonEncoding encapsulates json encoding and decoding
type jsonEncoding struct {
}

// Marshal encodes an array of object into bytes
func (g jsonEncoding) Marshal(objs []interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for i, obj := range objs {
		if err := enc.Encode(obj); err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("missing argument at index %d of type %T", i, obj)
			}
			return nil, fmt.Errorf(
				"unable to encode argument: %d, %v, with json error: %v", i, reflect.TypeOf(obj), err)
		}
	}
	return buf.Bytes(), nil
}

// Unmarshal decodes a byte array into the passed in objects
func (g jsonEncoding) Unmarshal(data []byte, objs []interface{}) error {
	dec := json.NewDecoder(bytes.NewBuffer(data))
	for i, obj := range objs {
		if err := dec.Decode(obj); err != nil {
			return fmt.Errorf(
				"unable to decode argument: %d, %v, with json error: %v", i, reflect.TypeOf(obj), err)
		}
	}
	return nil
}
