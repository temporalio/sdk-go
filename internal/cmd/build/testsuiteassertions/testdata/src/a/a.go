package a

import (
	req "github.com/stretchr/testify/require"
	testifysuite "github.com/stretchr/testify/suite"
	"importedsuite"
)

type validSuite struct {
	*req.Assertions
	testifysuite.Suite
}

func (s *validSuite) SetupTest() {
	s.Assertions = req.New(s.T())
}

type parenthesizedSuite struct {
	*req.Assertions
	testifysuite.Suite
}

func (s *parenthesizedSuite) SetupTest() {
	(s.Assertions) = (req.New(s.T()))
}

type valueEmbeddedAssertionsSuite struct {
	req.Assertions
	testifysuite.Suite
}

func (s *valueEmbeddedAssertionsSuite) SetupTest() {
	s.Assertions = *req.New(s.T())
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

type valueReceiverSuite struct {
	*req.Assertions
	testifysuite.Suite
}

func (s valueReceiverSuite) SetupTest() { // want "valueReceiverSuite.SetupTest must have signature func \\(\\*valueReceiverSuite\\) SetupTest\\(\\)"
	s.Assertions = req.New(s.T())
}

type invalidSignatureSuite struct {
	*req.Assertions
	testifysuite.Suite
}

func (s *invalidSignatureSuite) SetupTest(_ int) { // want "invalidSignatureSuite.SetupTest must have signature func \\(\\*invalidSignatureSuite\\) SetupTest\\(\\)"
	s.Assertions = req.New(s.T())
}

type conditionalRebindSuite struct {
	*req.Assertions
	testifysuite.Suite
}

func (s *conditionalRebindSuite) SetupTest() { // want "conditionalRebindSuite.SetupTest must rebind embedded require.Assertions"
	if true {
		s.Assertions = req.New(s.T())
	}
}

type delayedRebindSuite struct {
	*req.Assertions
	testifysuite.Suite
}

func (s *delayedRebindSuite) SetupTest() { // want "delayedRebindSuite.SetupTest must rebind embedded require.Assertions"
	_ = s.T()
	s.Assertions = req.New(s.T())
}

type unreachableRebindSuite struct {
	*req.Assertions
	testifysuite.Suite
}

func (s *unreachableRebindSuite) SetupTest() { // want "unreachableRebindSuite.SetupTest must rebind embedded require.Assertions"
	return
	s.Assertions = req.New(s.T())
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

type aliasCannotSuppressSuite struct { // want "aliasCannotSuppressSuite embeds require.Assertions and suite.Suite; add SetupTest"
	*req.Assertions
	testifysuite.Suite
}

//testsuiteassertions:ignore aliases cannot define SetupTest
type ignoredSuiteAlias = aliasCannotSuppressSuite

type importedSuiteAlias = importedsuite.Suite

//testsuiteassertions:ignore assertions are deliberately suite-scoped
type ignoredSuite struct {
	*req.Assertions
	testifysuite.Suite
}

//testsuiteassertions:ignore
type ignoreWithoutReasonSuite struct { // want "//testsuiteassertions:ignore requires a reason"
	*req.Assertions
	testifysuite.Suite
}

type unrelatedAssertions struct{}

type unrelatedSuite struct {
	*unrelatedAssertions
	testifysuite.Suite
}
