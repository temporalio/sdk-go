package test_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	runtimemetrics "runtime/metrics"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	sdkmetrics "go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	stickyCacheProfileEnabledEnv    = "TEMPORAL_STICKY_CACHE_PROFILE"
	stickyCacheProfileChildEnv      = "TEMPORAL_STICKY_CACHE_PROFILE_CHILD"
	stickyCacheProfileScenarioEnv   = "TEMPORAL_STICKY_CACHE_PROFILE_SCENARIO"
	stickyCacheProfileTrialEnv      = "TEMPORAL_STICKY_CACHE_PROFILE_TRIAL"
	stickyCacheProfileDirEnv        = "TEMPORAL_STICKY_CACHE_PROFILE_DIR"
	stickyCacheProfileTrialsEnv     = "TEMPORAL_STICKY_CACHE_PROFILE_TRIALS"
	stickyCacheProfileWorkflowsEnv  = "TEMPORAL_STICKY_CACHE_PROFILE_WORKFLOWS"
	stickyCacheProfileStateBytesEnv = "TEMPORAL_STICKY_CACHE_PROFILE_STATE_BYTES"
	stickyCacheProfileGCCyclesEnv   = "TEMPORAL_STICKY_CACHE_PROFILE_GC_CYCLES"

	stickyCacheProfileQuery = "sticky-cache-retention-ready"
)

type stickyCacheProfileMemory struct {
	LiveBytes       uint64 `json:"live_bytes"`
	HeapObjects     uint64 `json:"heap_objects"`
	ScannableBytes  uint64 `json:"scannable_bytes"`
	HeapObjectBytes uint64 `json:"heap_object_bytes"`
	ForcedGCCycles  uint64 `json:"forced_gc_cycles"`
}

type stickyCacheProfileGC struct {
	Cycles          uint64  `json:"cycles"`
	CPUSeconds      float64 `json:"cpu_seconds"`
	CPUSecondsPerGC float64 `json:"cpu_seconds_per_gc"`
	WallSeconds     float64 `json:"wall_seconds"`
}

type stickyCacheProfileResult struct {
	Scenario                string                   `json:"scenario"`
	Trial                   int                      `json:"trial"`
	WorkflowCount           int                      `json:"workflow_count"`
	WorkflowStateBytes      int                      `json:"workflow_state_bytes"`
	ObservedStickyCacheSize float64                  `json:"observed_sticky_cache_size"`
	Baseline                stickyCacheProfileMemory `json:"baseline"`
	AfterStop               stickyCacheProfileMemory `json:"after_stop"`
	GCWorkload              stickyCacheProfileGC     `json:"gc_workload"`
	WorkflowCoroutines      int                      `json:"workflow_coroutines_after_stop"`
	HeapProfile             string                   `json:"heap_profile"`
	GoVersion               string                   `json:"go_version"`
	GOOS                    string                   `json:"goos"`
	GOARCH                  string                   `json:"goarch"`
	GOMAXPROCS              int                      `json:"gomaxprocs"`
	GOGC                    string                   `json:"gogc"`
	GOMEMLIMIT              string                   `json:"gomemlimit"`
	GODEBUG                 string                   `json:"godebug"`
	SDKCommit               string                   `json:"sdk_commit"`
	ShutdownToSnapshot      time.Duration            `json:"shutdown_to_snapshot"`
}

type stickyCacheProfileExecution struct {
	workflowID string
	runID      string
}

func TestStickyCacheRetentionProfile(t *testing.T) {
	if os.Getenv(stickyCacheProfileChildEnv) == "1" {
		runStickyCacheProfileChild(t)
		return
	}
	if os.Getenv(stickyCacheProfileEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run the sticky-cache profiling experiment", stickyCacheProfileEnabledEnv)
	}

	rootDir, err := filepath.Abs("..")
	require.NoError(t, err)
	artifactRoot := filepath.Join(rootDir, ".sticky-cache-retention-artifacts")
	runDir := filepath.Join(artifactRoot, time.Now().UTC().Format("20060102T150405Z"))
	require.NoError(t, os.MkdirAll(runDir, 0777))

	probeBinary := filepath.Join(runDir, "sticky-cache-profile.test")
	compile := exec.Command("go", "test", "-c", "-o", probeBinary, ".")
	compileOutput, err := compile.CombinedOutput()
	require.NoError(t, err, "compile profiling subprocess:\n%s", compileOutput)
	t.Cleanup(func() { require.NoError(t, os.Remove(probeBinary)) })

	type manifestEntry struct {
		Scenario string `json:"scenario"`
		Trial    int    `json:"trial"`
		Log      string `json:"log"`
		Result   string `json:"result"`
	}
	var manifest []manifestEntry
	for trial := 1; trial <= stickyCacheProfileEnvInt(t, stickyCacheProfileTrialsEnv, 3); trial++ {
		for _, scenario := range []string{"disabled", "purged", "unpurged"} {
			logName := fmt.Sprintf("%s-%d.log", scenario, trial)
			resultName := fmt.Sprintf("%s-%d.json", scenario, trial)
			cmd := exec.Command(probeBinary, "-test.run=^TestStickyCacheRetentionProfile$", "-test.v")
			cmd.Env = append(os.Environ(),
				stickyCacheProfileChildEnv+"=1",
				stickyCacheProfileScenarioEnv+"="+scenario,
				stickyCacheProfileTrialEnv+"="+strconv.Itoa(trial),
				stickyCacheProfileDirEnv+"="+runDir,
			)
			output, runErr := cmd.CombinedOutput()
			require.NoError(t, os.WriteFile(filepath.Join(runDir, logName), output, 0666))
			require.NoError(t, runErr, "%s trial %d failed:\n%s", scenario, trial, output)
			manifest = append(manifest, manifestEntry{
				Scenario: scenario,
				Trial:    trial,
				Log:      logName,
				Result:   resultName,
			})
		}
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "manifest.json"), manifestBytes, 0666))
	t.Logf("sticky-cache profiling artifacts: %s", runDir)
}

func runStickyCacheProfileChild(t *testing.T) {
	runtime.MemProfileRate = 1

	scenario := os.Getenv(stickyCacheProfileScenarioEnv)
	require.Contains(t, []string{"disabled", "purged", "unpurged"}, scenario)
	trial, err := strconv.Atoi(os.Getenv(stickyCacheProfileTrialEnv))
	require.NoError(t, err)
	artifactDir := os.Getenv(stickyCacheProfileDirEnv)
	require.NotEmpty(t, artifactDir)

	workflowCount := stickyCacheProfileEnvInt(t, stickyCacheProfileWorkflowsEnv, 32)
	workflowStateBytes := stickyCacheProfileEnvInt(t, stickyCacheProfileStateBytesEnv, 1<<20)

	normalizeGC()
	baseline := readStickyCacheProfileMemory()
	executions, observedCacheSize, shutdownStarted := runStickyCacheProfileWorkload(
		t,
		scenario,
		trial,
		workflowCount,
		workflowStateBytes,
	)
	if scenario == "purged" {
		worker.PurgeStickyWorkflowCache()
	}
	if scenario != "unpurged" {
		require.Eventually(t, func() bool {
			return countStickyCacheProfileWorkflowCoroutines() == 0
		}, 20*time.Second, 20*time.Millisecond)
	}
	normalizeGC()
	afterStop := readStickyCacheProfileMemory()
	shutdownToSnapshot := time.Since(shutdownStarted)
	workflowCoroutines := countStickyCacheProfileWorkflowCoroutines()

	heapProfileName := fmt.Sprintf("%s-%d.pprof", scenario, trial)
	heapProfilePath := filepath.Join(artifactDir, heapProfileName)
	heapProfile, err := os.Create(heapProfilePath)
	require.NoError(t, err)
	require.NoError(t, pprof.WriteHeapProfile(heapProfile))
	require.NoError(t, heapProfile.Close())
	normalizeGC()
	gcWorkload := runStickyCacheProfileGCWorkload(
		stickyCacheProfileEnvInt(t, stickyCacheProfileGCCyclesEnv, 100),
		8<<20,
	)

	result := stickyCacheProfileResult{
		Scenario:                scenario,
		Trial:                   trial,
		WorkflowCount:           workflowCount,
		WorkflowStateBytes:      workflowStateBytes,
		ObservedStickyCacheSize: observedCacheSize,
		Baseline:                baseline,
		AfterStop:               afterStop,
		GCWorkload:              gcWorkload,
		WorkflowCoroutines:      workflowCoroutines,
		HeapProfile:             heapProfileName,
		GoVersion:               runtime.Version(),
		GOOS:                    runtime.GOOS,
		GOARCH:                  runtime.GOARCH,
		GOMAXPROCS:              runtime.GOMAXPROCS(0),
		GOGC:                    os.Getenv("GOGC"),
		GOMEMLIMIT:              os.Getenv("GOMEMLIMIT"),
		GODEBUG:                 os.Getenv("GODEBUG"),
		SDKCommit:               stickyCacheProfileSDKCommit(t),
		ShutdownToSnapshot:      shutdownToSnapshot,
	}
	resultBytes, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	resultPath := filepath.Join(artifactDir, fmt.Sprintf("%s-%d.json", scenario, trial))
	require.NoError(t, os.WriteFile(resultPath, resultBytes, 0666))

	cleanupStickyCacheProfileExecutions(t, executions)
}

func runStickyCacheProfileWorkload(
	t *testing.T,
	scenario string,
	trial int,
	workflowCount int,
	workflowStateBytes int,
) ([]stickyCacheProfileExecution, float64, time.Time) {
	cacheSize := workflowCount + 2
	if scenario == "disabled" {
		cacheSize = 0
	}
	worker.SetStickyWorkflowCacheSize(cacheSize)

	metricsHandler := sdkmetrics.NewCapturingHandler()
	namespace := os.Getenv("TEMPORAL_NAMESPACE")
	if namespace == "" {
		namespace = client.DefaultNamespace
	}
	hostPort := os.Getenv("TEMPORAL_ADDRESS")
	if hostPort == "" {
		hostPort = client.DefaultHostPort
	}
	c, err := client.Dial(client.Options{
		HostPort:       hostPort,
		Namespace:      namespace,
		MetricsHandler: metricsHandler,
		Logger:         ilog.NewNopLogger(),
	})
	require.NoError(t, err)
	clientClosed := false
	defer func() {
		if !clientClosed {
			c.Close()
		}
	}()

	taskQueue := fmt.Sprintf("sticky-cache-profile-%s-%d-%d", scenario, trial, os.Getpid())
	w := worker.New(c, taskQueue, worker.Options{DisableEagerActivities: true})
	w.RegisterWorkflow(stickyCacheRetentionProfileWorkflow)
	require.NoError(t, w.Start())
	workerStopped := false
	defer func() {
		if !workerStopped {
			w.Stop()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	executions := make([]stickyCacheProfileExecution, 0, workflowCount)
	for i := 0; i < workflowCount; i++ {
		workflowID := fmt.Sprintf("%s-workflow-%d", taskQueue, i)
		run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: taskQueue,
		}, stickyCacheRetentionProfileWorkflow, workflowStateBytes)
		require.NoError(t, err)
		executions = append(executions, stickyCacheProfileExecution{
			workflowID: run.GetID(),
			runID:      run.GetRunID(),
		})
	}

	for _, execution := range executions {
		require.Eventually(t, func() bool {
			value, err := c.QueryWorkflow(
				ctx,
				execution.workflowID,
				execution.runID,
				stickyCacheProfileQuery,
			)
			if err != nil {
				return false
			}
			var size int
			return value.Get(&size) == nil && size == workflowStateBytes
		}, 30*time.Second, 50*time.Millisecond)
	}

	observedCacheSize := stickyCacheProfileGaugeValue(metricsHandler)
	if scenario == "disabled" {
		require.Equal(t, float64(0), observedCacheSize)
	} else {
		require.Eventually(t, func() bool {
			observedCacheSize = stickyCacheProfileGaugeValue(metricsHandler)
			return observedCacheSize >= float64(workflowCount)
		}, 30*time.Second, 50*time.Millisecond)
	}

	shutdownStarted := time.Now()
	w.Stop()
	workerStopped = true
	w = nil
	c.Close()
	clientClosed = true
	c = nil
	metricsHandler = nil
	return executions, observedCacheSize, shutdownStarted
}

func stickyCacheRetentionProfileWorkflow(ctx workflow.Context, stateSize int) error {
	state := make([]byte, stateSize)
	state[len(state)-1] = 1
	if err := workflow.SetQueryHandler(ctx, stickyCacheProfileQuery, func() (int, error) {
		return len(state), nil
	}); err != nil {
		return err
	}
	return workflow.Await(ctx, func() bool { return state[len(state)-1] == 2 })
}

func stickyCacheProfileGaugeValue(handler *sdkmetrics.CapturingHandler) float64 {
	var value float64
	for _, gauge := range handler.Gauges() {
		if gauge.Name == sdkmetrics.StickyCacheSize && gauge.Value() > value {
			value = gauge.Value()
		}
	}
	return value
}

func normalizeGC() {
	runtime.GC()
	runtime.Gosched()
	runtime.GC()
}

func readStickyCacheProfileMemory() stickyCacheProfileMemory {
	names := []string{
		"/gc/heap/live:bytes",
		"/gc/heap/objects:objects",
		"/gc/scan/heap:bytes",
		"/memory/classes/heap/objects:bytes",
		"/gc/cycles/forced:gc-cycles",
	}
	samples := make([]runtimemetrics.Sample, len(names))
	for i, name := range names {
		samples[i].Name = name
	}
	runtimemetrics.Read(samples)
	return stickyCacheProfileMemory{
		LiveBytes:       samples[0].Value.Uint64(),
		HeapObjects:     samples[1].Value.Uint64(),
		ScannableBytes:  samples[2].Value.Uint64(),
		HeapObjectBytes: samples[3].Value.Uint64(),
		ForcedGCCycles:  samples[4].Value.Uint64(),
	}
}

func runStickyCacheProfileGCWorkload(cycles int, allocationBytes int) stickyCacheProfileGC {
	previousGCPercent := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGCPercent)

	beforeCPU, beforeCycles := readStickyCacheProfileGC()
	started := time.Now()
	for i := 0; i < cycles; i++ {
		allocation := make([]byte, allocationBytes)
		allocation[len(allocation)-1] = byte(i)
		runtime.KeepAlive(allocation)
		runtime.GC()
	}
	wall := time.Since(started).Seconds()
	afterCPU, afterCycles := readStickyCacheProfileGC()
	cycleDelta := afterCycles - beforeCycles
	cpuDelta := afterCPU - beforeCPU
	var perCycle float64
	if cycleDelta > 0 {
		perCycle = cpuDelta / float64(cycleDelta)
	}
	return stickyCacheProfileGC{
		Cycles:          cycleDelta,
		CPUSeconds:      cpuDelta,
		CPUSecondsPerGC: perCycle,
		WallSeconds:     wall,
	}
}

func readStickyCacheProfileGC() (cpuSeconds float64, cycles uint64) {
	samples := []runtimemetrics.Sample{
		{Name: "/cpu/classes/gc/total:cpu-seconds"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
	runtimemetrics.Read(samples)
	return samples[0].Value.Float64(), samples[1].Value.Uint64()
}

func countStickyCacheProfileWorkflowCoroutines() int {
	stack := make([]byte, 8<<20)
	n := runtime.Stack(stack, true)
	return strings.Count(
		string(stack[:n]),
		"go.temporal.io/sdk/internal.(*coroutineState).initialYield",
	)
}

func cleanupStickyCacheProfileExecutions(t *testing.T, executions []stickyCacheProfileExecution) {
	namespace := os.Getenv("TEMPORAL_NAMESPACE")
	if namespace == "" {
		namespace = client.DefaultNamespace
	}
	hostPort := os.Getenv("TEMPORAL_ADDRESS")
	if hostPort == "" {
		hostPort = client.DefaultHostPort
	}
	c, err := client.Dial(client.Options{
		HostPort:  hostPort,
		Namespace: namespace,
		Logger:    ilog.NewNopLogger(),
	})
	require.NoError(t, err)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, execution := range executions {
		err := c.TerminateWorkflow(
			ctx,
			execution.workflowID,
			execution.runID,
			"sticky-cache profiling cleanup",
		)
		require.NoError(t, err)
	}
}

func stickyCacheProfileEnvInt(t *testing.T, name string, defaultValue int) int {
	value := os.Getenv(name)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	require.NoError(t, err, "parse %s", name)
	require.Positive(t, parsed, name)
	return parsed
}

func stickyCacheProfileSDKCommit(t *testing.T) string {
	output, err := exec.Command("git", "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(output))
}
