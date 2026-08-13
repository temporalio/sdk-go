package a

import (
	req "github.com/stretchr/testify/require"
	testifysuite "github.com/stretchr/testify/suite"
)

type validSuite struct {
	*req.Assertions
	testifysuite.Suite
}

func (s *validSuite) SetupTest() {
	s.Assertions = req.New(s.T())
}

type missingSetupTestSuite struct { // want "missingSetupTestSuite embeds require.Assertions and suite.Suite; add SetupTest"
	*req.Assertions
	testifysuite.Suite
}

// PayloadLimitsTestSuite models the stale assertion lifecycle fixed by sdk-go PR #2333.
type PayloadLimitsTestSuite struct {
	*req.Assertions
	testifysuite.Suite
}

func (ts *PayloadLimitsTestSuite) SetupSuite() {
	ts.Assertions = req.New(ts.T())
}

func (ts *PayloadLimitsTestSuite) SetupTest() { // want "PayloadLimitsTestSuite.SetupTest must rebind embedded require.Assertions"
	_ = ts.T()
}

type wrongTestSuite struct {
	*req.Assertions
	testifysuite.Suite
}

func (s *wrongTestSuite) SetupTest() { // want "wrongTestSuite.SetupTest must rebind embedded require.Assertions"
	var other testifysuite.Suite
	s.Assertions = req.New(other.T())
}

type unnamedReceiverSuite struct {
	*req.Assertions
	testifysuite.Suite
}

func (*unnamedReceiverSuite) SetupTest() { // want "unnamedReceiverSuite.SetupTest must rebind embedded require.Assertions"
}

type AssertionsAlias = req.Assertions
type SuiteAlias = testifysuite.Suite

type aliasSuite struct {
	*AssertionsAlias
	SuiteAlias
}

func (s *aliasSuite) SetupTest() {
	s.AssertionsAlias = req.New(s.T())
}

//testsuiteassertions:ignore assertions are deliberately suite-scoped
type ignoredSuite struct {
	*req.Assertions
	testifysuite.Suite
}

//testsuiteassertions:ignore
type ignoreWithoutReasonSuite struct { // want "ignoreWithoutReasonSuite embeds require.Assertions and suite.Suite; add SetupTest"
	*req.Assertions
	testifysuite.Suite
}

type unrelatedAssertions struct{}

type unrelatedSuite struct {
	*unrelatedAssertions
	testifysuite.Suite
}
