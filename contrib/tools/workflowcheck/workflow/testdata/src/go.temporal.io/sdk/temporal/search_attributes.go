package temporal

// This file somewhat mirrors the internal SearchAttributes implementation. The purpose of this is to
// test SearchAttributes.Copy()'s map iteration being ignored with `//workflowcheck:ignore`

type (
	SearchAttributeKey interface{}

	// SearchAttributes represents a collection of typed search attributes
	SearchAttributes struct {
		untypedValue map[SearchAttributeKey]interface{}
	}

	// SearchAttributeUpdate represents a change to SearchAttributes
	SearchAttributeUpdate func(*SearchAttributes)
)

// GetUntypedValues gets a copy of the collection with raw types.
func (sa SearchAttributes) GetUntypedValues() map[SearchAttributeKey]interface{} {
	untypedValueCopy := make(map[SearchAttributeKey]interface{}, len(sa.untypedValue))
	for key, value := range sa.untypedValue {
		// Filter out nil values
		if value == nil {
			continue
		}
		switch v := value.(type) {
		case []string:
			untypedValueCopy[key] = append([]string(nil), v...)
		default:
			untypedValueCopy[key] = v
		}
	}
	return untypedValueCopy
}

// Copy creates an update that copies existing values.
//
//workflowcheck:ignore
func (sa SearchAttributes) Copy() SearchAttributeUpdate {
	return func(s *SearchAttributes) {
		// GetUntypedValues returns a copy of the map without nil values
		// so the copy won't delete any existing values
		untypedValues := sa.GetUntypedValues()
		for key, value := range untypedValues {
			s.untypedValue[key] = value
		}
	}
}
