package internal

import (
	"encoding/binary"
	"encoding/hex"
	"math/rand/v2"
	"testing"
	"time"

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

	randoms := make(map[string]*workflowRandomStream)
	randomBytes := make([]byte, 32)

	n, err := getRandomStream(randoms, workflowRandomTestRunID, workflowRandomTestName).Read(randomBytes)
	s.Require().NoError(err)
	s.Require().Equal(len(randomBytes), n)
	s.Require().Equal("10861bf410d33891bef9b1f2ebddc1af2f5bceffe86c13fdcb8534a08805b1a7", hex.EncodeToString(randomBytes))
}

// TestUint64Golden pins Uint64's value for a known seed. Changing the
// Uint64/Read derivation would break replay for existing workflows.
func (s *workflowRandomTestSuite) TestUint64Golden() {
	randoms := make(map[string]*workflowRandomStream)
	v := getRandomStream(randoms, workflowRandomTestRunID, workflowRandomTestName).Uint64()
	s.Require().Equal(uint64(0x9138d310f41b8610), v)
}

// TestInterleavedReadUint64StableOrdering verifies that interleaving Read and
// Uint64 calls consumes the underlying byte stream in the same order, and
// with the same byte boundaries, as a single equivalent-length Read: Uint64
// must be equivalent to reading 8 bytes and decoding them as little-endian,
// not a separate, independently-buffered draw from the source. This is the
// stability guarantee tracked by
// https://github.com/temporalio/sdk-go/issues/2547.
func (s *workflowRandomTestSuite) TestInterleavedReadUint64StableOrdering() {
	randomsInterleaved := make(map[string]*workflowRandomStream)
	c := getRandomStream(randomsInterleaved, workflowRandomTestRunID, workflowRandomTestName)

	read1 := make([]byte, 8)
	_, err := c.Read(read1)
	s.Require().NoError(err)

	u := c.Uint64()
	uBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(uBytes, u)

	read2 := make([]byte, 8)
	_, err = c.Read(read2)
	s.Require().NoError(err)

	reconstructed := append(append(append([]byte{}, read1...), uBytes...), read2...)

	randomsFull := make(map[string]*workflowRandomStream)
	full := make([]byte, 24)
	_, err = getRandomStream(randomsFull, workflowRandomTestRunID, workflowRandomTestName).Read(full)
	s.Require().NoError(err)

	s.Require().Equal(full, reconstructed)
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
	randoms := make(map[string]*workflowRandomStream)

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

		randoms := make(map[string]*workflowRandomStream)
		c := getRandomStream(randoms, workflowRandomTestRunID, name)
		r := rand.New(c)

		for range draws {
			res = append(res, r.Uint64())
		}
		return res
	}

	var interleavedA, interleavedB []uint64

	randoms := make(map[string]*workflowRandomStream)
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
	randoms := make(map[string]*workflowRandomStream)

	c1 := getRandomStream(randoms, workflowRandomTestRunID, workflowRandomTestName)
	reseedRandoms(randoms, "other")
	c2 := getRandomStream(randoms, "other", workflowRandomTestName)

	s.Require().Same(c1, c2)
}

func workflowRandomContinueAsNewWorkflow(ctx Context, prev int) ([]int, error) {
	current := rand.New(GetRandomStream(ctx, workflowRandomTestName)).Int()

	if prev == 0 {
		return nil, NewContinueAsNewError(ctx, workflowRandomContinueAsNewWorkflow, current)
	}

	return []int{prev, current}, nil
}

func workflowRandomParentWorkflow(ctx Context) ([]int, error) {
	parent := rand.New(GetRandomStream(ctx, workflowRandomTestName)).Int()

	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		WorkflowRunTimeout: time.Minute,
	})

	var child []int
	err := ExecuteChildWorkflow(ctx, workflowRandomContinueAsNewWorkflow, 0).Get(ctx, &child)
	if err != nil {
		return nil, err
	}

	return append([]int{parent}, child...), nil
}

func (s *workflowRandomTestSuite) TestChildContinueAsNewDrawsNewValues() {
	testSuite := WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowRandomParentWorkflow)
	env.RegisterWorkflow(workflowRandomContinueAsNewWorkflow)

	env.ExecuteWorkflow(workflowRandomParentWorkflow)
	s.Require().True(env.IsWorkflowCompleted())
	s.Require().NoError(env.GetWorkflowError())

	var result []int
	s.Require().NoError(env.GetWorkflowResult(&result))
	s.Require().Len(result, 3)
	s.Require().NotEqual(result[0], result[1])
	s.Require().NotEqual(result[0], result[2])
	s.Require().NotEqual(result[1], result[2])
}
