package internal

import (
	"reflect"
)

type (
	// Value is used to encapsulate/extract encoded value from workflow/activity.
	Value interface {
		// HasValue return whether there is value encoded.
		HasValue() bool
		// Get extract the encoded value into strong typed value pointer.
		Get(valuePtr interface{}) error
	}

	// Values is used to encapsulate/extract encoded one or more values from workflow/activity.
	Values interface {
		// HasValues return whether there are values encoded.
		HasValues() bool
		// Get extract the encoded values into strong typed value pointers.
		Get(valuePtr ...interface{}) error
	}

	// DataConverter is used by the framework to serialize/deserialize input and output of activity/workflow
	// that need to be sent over the wire.
	// To encode/decode workflow arguments, one should set DataConverter in two places:
	//   1. Workflow worker, through worker.Options
	//   2. Client, through client.Options
	// To encode/decode Activity/ChildWorkflow arguments, one should set DataConverter in two places:
	//   1. Inside workflow code, use workflow.WithDataConverter to create new Context,
	// and pass that context to ExecuteActivity/ExecuteChildWorkflow calls.
	// Temporal support using different DataConverters for different activity/childWorkflow in same workflow.
	//   2. Activity/Workflow worker that run these activity/childWorkflow, through worker.Options.
	DataConverter interface {
		// ToData implements conversion of a list of values.
		ToData(value ...interface{}) ([]byte, error)
		// FromData implements conversion of an array of values of different types.
		// Useful for deserializing arguments of function invocations.
		FromData(input []byte, valuePtr ...interface{}) error
	}

	// defaultDataConverter uses thrift encoder/decoder when possible, for everything else use json.
	defaultDataConverter struct{}
)

var defaultJSONDataConverter = &defaultDataConverter{}

// DefaultDataConverter is default data converter used by Temporal worker
var DefaultDataConverter = getDefaultDataConverter()

// getDefaultDataConverter return default data converter used by Temporal worker
func getDefaultDataConverter() DataConverter {
	return defaultJSONDataConverter
}

func (dc *defaultDataConverter) ToData(r ...interface{}) ([]byte, error) {
	if len(r) == 1 && isTypeByteSlice(reflect.TypeOf(r[0])) {
		return r[0].([]byte), nil
	}

	encoder := &jsonEncoding{}

	data, err := encoder.Marshal(r)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (dc *defaultDataConverter) FromData(data []byte, to ...interface{}) error {
	if len(to) == 1 && isTypeByteSlice(reflect.TypeOf(to[0])) {
		reflect.ValueOf(to[0]).Elem().SetBytes(data)
		return nil
	}

	encoder := &jsonEncoding{}

	return encoder.Unmarshal(data, to)
}
