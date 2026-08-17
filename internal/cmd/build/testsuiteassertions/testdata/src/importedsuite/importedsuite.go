package importedsuite

import (
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type Suite struct {
	*require.Assertions
	suite.Suite
}
