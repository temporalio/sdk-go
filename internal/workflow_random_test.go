package internal

import (
	"encoding/hex"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	workflowRandomTestRunID = "runID"
	workflowRandomTestName  = "go.temporal.io/sdk/internal/test"
)

type workflowRandomTestSuite struct {
	suite.Suite
}

func TestWorkflowRandomTestSuite(t *testing.T) {
	suite.Run(t, new(workflowRandomTestSuite))
}

func (s *workflowRandomTestSuite) TestDeriveSeed() {
	s.Require().Equal(deriveSeed(workflowRandomTestRunID, workflowRandomTestName), deriveSeed(workflowRandomTestRunID, workflowRandomTestName))

	cases := []struct {
		runID string
		name  string
	}{
		{runID: "other", name: workflowRandomTestName},
		{runID: workflowRandomTestRunID, name: "other"},
		{runID: "other", name: "other"},
	}

	for _, tc := range cases {
		s.Require().NotEqual(deriveSeed(workflowRandomTestRunID, workflowRandomTestName), deriveSeed(tc.runID, tc.name))
	}
}

// TestGetRandomStreamGolden pins the seed derivation and resulting byte stream.
// Changing either would break replay for existing workflows.
func (s *workflowRandomTestSuite) TestGetRandomStreamGolden() {
	seed := deriveSeed(workflowRandomTestRunID, workflowRandomTestName)
	s.Require().Equal("0d143739fa5a902590bac3b5bff5f52b539f57ae2ba4cf0ab3b034623b1da7ec", hex.EncodeToString(seed[:]))

	randoms := make(map[string]*rand.ChaCha8)
	randomBytes := make([]byte, 32)

	n, err := getRandomStream(randoms, workflowRandomTestRunID, workflowRandomTestName).Read(randomBytes)
	s.Require().NoError(err)
	s.Require().Equal(len(randomBytes), n)
	s.Require().Equal("10861bf410d33891bef9b1f2ebddc1af2f5bceffe86c13fdcb8534a08805b1a7", hex.EncodeToString(randomBytes))
}

func (s *workflowRandomTestSuite) TestDeriveSeedSeparators() {
	s.Require().NotEqual(
		deriveSeed("ab", "c"),
		deriveSeed("a", "bc"),
	)
}

// TestGetRandomStreamMemoizes verifies a second lookup under the same name continues
// the sequence rather than restarting it.
func (s *workflowRandomTestSuite) TestGetRandomStreamMemoizes() {
	randoms := make(map[string]*rand.ChaCha8)

	c1 := getRandomStream(randoms, workflowRandomTestRunID, workflowRandomTestName)
	firstDraw := c1.Uint64()

	c2 := getRandomStream(randoms, workflowRandomTestRunID, workflowRandomTestName)
	secondDraw := c2.Uint64()

	s.Require().Same(c1, c2)
	s.Require().NotEqual(firstDraw, secondDraw)
}

// TestGetRandomStreamNamesAreIndependent verifies that interleaving draws across two
// names yields the same sequence per name as drawing from each on its own, so
// how often a workflow draws from one name cannot shift another.
func (s *workflowRandomTestSuite) TestGetRandomStreamNamesAreIndependent() {

	solo := func(name string, draws int) []uint64 {
		var res []uint64

		randoms := make(map[string]*rand.ChaCha8)
		c := getRandomStream(randoms, workflowRandomTestRunID, name)
		r := rand.New(c)

		for range draws {
			res = append(res, r.Uint64())
		}
		return res
	}

	var interleavedA, interleavedB []uint64

	randoms := make(map[string]*rand.ChaCha8)
	c1 := getRandomStream(randoms, workflowRandomTestRunID, workflowRandomTestName)
	c2 := getRandomStream(randoms, workflowRandomTestRunID, "other")

	for range 3 {
		interleavedA = append(interleavedA, c1.Uint64())
		interleavedB = append(interleavedB, c2.Uint64())
	}

	s.Require().Equal(solo(workflowRandomTestName, 3), interleavedA)
	s.Require().Equal(solo("other", 3), interleavedB)
	s.Require().NotEqual(interleavedA, interleavedB)
}

func (s *workflowRandomTestSuite) TestReseedRandomsInPlace() {
	randoms := make(map[string]*rand.ChaCha8)

	c1 := getRandomStream(randoms, workflowRandomTestRunID, workflowRandomTestName)
	reseedRandoms(randoms, "other")
	c2 := getRandomStream(randoms, "other", workflowRandomTestName)

	s.Require().Same(c1, c2)
}
