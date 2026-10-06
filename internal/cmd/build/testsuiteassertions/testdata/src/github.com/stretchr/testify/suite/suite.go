package suite

import "testing"

type Suite struct{}

func (*Suite) T() *testing.T { return nil }
