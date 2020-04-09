package common

import (
	"math"
)

// Int32Ceil return the int32 ceil of a float64
func Int32Ceil(v float64) int32 {
	return int32(math.Ceil(v))
}

// Int64Ceil return the int64 ceil of a float64
func Int64Ceil(v float64) int64 {
	return int64(math.Ceil(v))
}
