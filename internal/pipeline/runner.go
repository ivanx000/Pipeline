package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fatih/color"
)

var (
	info    = color.New(color.FgCyan).Add(color.Bold)
	warn    = color.New(color.FgYellow).Add(color.Bold)
	success = color.New(color.FgGreen).Add(color.Bold)
)

// Options controls runtime behavior for pipeline execution.
type Options struct {
	Quiet           bool
	From            string
	Only            []string
	SummaryJSONPath string
	MaxParallel     int
}

// StageSummary stores one stage execution result for JSON reporting.
type StageSummary struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	Status     string `json:"status"`
	Attempts   int    `json:"attempts"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// RunSummary stores a whole pipeline execution result for JSON reporting.
type RunSummary struct {
	Project     string         `json:"project"`
	ConfigPath  string         `json:"config_path"`
	Status      string         `json:"status"`
	StartedAt   time.Time      `json:"started_at"`
	FinishedAt  time.Time      `json:"finished_at"`
	DurationMS  int64          `json:"duration_ms"`
	MaxParallel int            `json:"max_parallel"`
	StageCount  int            `json:"stage_count"`
	Error       string         `json:"error,omitempty"`
	Stages      []StageSummary `json:"stages"`
}

type summaryRecorder struct {
	mu      sync.Mutex
	summary RunSummary
}

func newSummaryRecorder(project string, configPath string, maxParallel int, stageCount int) *summaryRecorder {
	return &summaryRecorder{
		summary: RunSummary{
			Project:     project,
			ConfigPath:  configPath,
			Status:      "running",
			StartedAt:   time.Now().UTC(),
			MaxParallel: maxParallel,
			StageCount:  stageCount,
		},
	}
}

func (r *summaryRecorder) addStage(stage StageSummary) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.summary.Stages = append(r.summary.Stages, stage)
}

func (r *summaryRecorder) finish(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.summary.FinishedAt = time.Now().UTC()
	r.summary.DurationMS = r.summary.FinishedAt.Sub(r.summary.StartedAt).Milliseconds()
	if err != nil {
		r.summary.Status = "failed"
		r.summary.Error = err.Error()
		return
	}
	r.summary.Status = "success"
}

func (r *summaryRecorder) snapshot() RunSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	stages := make([]StageSummary, len(r.summary.Stages))
	copy(stages, r.summary.Stages)
	result := r.summary
	result.Stages = stages
	return result
}

// Run executes the pipeline from the provided config path.
func Run(configPath string) error {
	return RunWithOptions(configPath, Options{})
}

// RunWithOptions executes the pipeline from the provided config path using runtime options.
func RunWithOptions(configPath string, options Options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return run(ctx, configPath, options)
}

func run(ctx context.Context, configPath string, options Options) error {
	config, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	stages, err := selectStages(config.Stages, options)
	if err != nil {
		return err
	}

	maxParallel := config.MaxParallel
	if options.MaxParallel > 0 {
		maxParallel = options.MaxParallel
	}

	recorder := newSummaryRecorder(config.Project, configPath, maxParallel, len(stages))
	defer func() {
		if options.SummaryJSONPath == "" {
			return
		}
		if err := writeSummaryJSON(options.SummaryJSONPath, recorder.snapshot()); err != nil {
			warn.Printf("WARN  | Failed to write summary JSON %q: %v\n", options.SummaryJSONPath, err)
		}
	}()

	printInfo(options, "INFO  | Starting pipeline: %s\n", config.Project)
	if maxParallel > 0 {
		printInfo(options, "INFO  | Max parallel stages: %d\n", maxParallel)
	}
	if !options.Quiet {
		fmt.Println(strings.Repeat("=", 40))
	}
	err = runPipeline(ctx, stages, maxParallel, options, recorder)
	recorder.finish(err)
	if err != nil {
		return err
	}

	if !options.Quiet {
		fmt.Println(strings.Repeat("=", 40))
	}
	printSuccess(options, "INFO  | Pipeline completed successfully.\n")
	return nil
}

func runPipeline(ctx context.Context, stages []Stage, maxParallel int, options Options, recorder *summaryRecorder) error {
	var parallelBatch []Stage

	flushParallelBatch := func() error {
		if len(parallelBatch) == 0 {
			return nil
		}

		err := runParallelBatch(ctx, parallelBatch, maxParallel, options, recorder)
		parallelBatch = nil
		return err
	}

	for _, stage := range stages {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("pipeline cancelled: %w", err)
		}

		if stage.Parallel {
			parallelBatch = append(parallelBatch, stage)
		} else {
			if err := flushParallelBatch(); err != nil {
				return err
			}

			if err := executeStageWithRetryTracked(ctx, stage, options, recorder); err != nil {
				return err
			}
		}
	}

	if err := flushParallelBatch(); err != nil {
		return err
	}

	return nil
}

func runParallelBatch(parentCtx context.Context, stages []Stage, maxParallel int, options Options, recorder *summaryRecorder) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	type stageResult struct {
		err error
	}

	resultCh := make(chan stageResult, len(stages))
	var wg sync.WaitGroup

	var sem chan struct{}
	if maxParallel > 0 {
		sem = make(chan struct{}, maxParallel)
	}

	for _, stage := range stages {
		wg.Add(1)
		go func(s Stage) {
			defer wg.Done()
			if sem != nil {
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					resultCh <- stageResult{err: &stageError{
						Stage:     s.Name,
						Command:   s.Command,
						Err:       ctx.Err(),
						Cancelled: true,
					}}
					return
				}
				defer func() { <-sem }()
			}

			defer func() {
				if r := recover(); r != nil {
					resultCh <- stageResult{err: &stageError{
						Stage:   s.Name,
						Command: s.Command,
						Err:     fmt.Errorf("panic: %v", r),
					}}
					cancel()
				}
			}()

			err := executeStageWithRetryTracked(ctx, s, options, recorder)
			if err != nil {
				cancel()
			}
			resultCh <- stageResult{err: err}
		}(stage)
	}

	wg.Wait()
	close(resultCh)

	var firstErr error
	for result := range resultCh {
		if result.err == nil {
			continue
		}

		if firstErr == nil {
			firstErr = result.err
			continue
		}

		var current *stageError
		var best *stageError
		if errors.As(result.err, &current) && errors.As(firstErr, &best) && best.Cancelled && !current.Cancelled {
			firstErr = result.err
		}
	}

	return firstErr
}

func executeStageWithRetryTracked(ctx context.Context, s Stage, options Options, recorder *summaryRecorder) error {
	started := time.Now()
	attempts, err := executeStageWithRetry(ctx, s, options)
	if recorder != nil {
		recorder.addStage(StageSummary{
			Name:       s.Name,
			Command:    strings.TrimSpace(strings.Join(append([]string{s.Command}, s.Args...), " ")),
			Status:     stageStatus(err),
			Attempts:   attempts,
			DurationMS: time.Since(started).Milliseconds(),
			Error:      errorMessage(err),
		})
	}
	return err
}

func executeStageWithRetry(ctx context.Context, s Stage, options Options) (int, error) {
	attempts := s.Retry + 1
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		err := executeStage(ctx, s, attempt, attempts, options)
		if err == nil {
			return attempt, nil
		}

		lastErr = err

		var sErr *stageError
		if errors.As(err, &sErr) && sErr.Cancelled {
			return attempt, err
		}
		if ctx.Err() != nil {
			return attempt, err
		}
		if attempt == attempts {
			break
		}

		warn.Printf("WARN  | Retrying stage %q (attempt %d/%d)\n", s.Name, attempt+1, attempts)
		if err := sleepWithContext(ctx, time.Second); err != nil {
			return attempt, &stageError{
				Stage:     s.Name,
				Command:   s.Command,
				Err:       err,
				Cancelled: true,
			}
		}
	}

	return attempts, lastErr
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// executeStage handles one stage attempt.
func executeStage(ctx context.Context, s Stage, attempt int, totalAttempts int, options Options) error {
	runLabel := s.Name
	if totalAttempts > 1 {
		runLabel = fmt.Sprintf("%s (attempt %d/%d)", s.Name, attempt, totalAttempts)
	}

	printInfo(options, "\nINFO  | Running stage: %s\n", runLabel)
	printInfo(options, "INFO  | Command: %s %s\n", s.Command, strings.Join(s.Args, " "))
	if s.Workdir != "" {
		printInfo(options, "INFO  | Workdir: %s\n", s.Workdir)
	}
	if len(s.Env) > 0 {
		printInfo(options, "INFO  | Env overrides: %d\n", len(s.Env))
	}
	if s.Timeout != "" {
		printInfo(options, "INFO  | Timeout: %s\n", s.Timeout)
	}

	runCtx := ctx
	cancel := func() {}
	if s.Timeout != "" {
		timeout, err := time.ParseDuration(s.Timeout)
		if err != nil {
			return &stageError{Stage: s.Name, Command: s.Command, Err: err}
		}
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, s.Command, s.Args...)
	if s.Workdir != "" {
		cmd.Dir = s.Workdir
	}
	if len(s.Env) > 0 {
		cmd.Env = mergedEnv(os.Environ(), s.Env)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return &stageError{
				Stage:   s.Name,
				Command: s.Command,
				Err:     fmt.Errorf("timed out after %s", s.Timeout),
			}
		}

		if ctx.Err() != nil {
			return &stageError{
				Stage:     s.Name,
				Command:   s.Command,
				Err:       ctx.Err(),
				Cancelled: true,
			}
		}

		return &stageError{
			Stage:   s.Name,
			Command: s.Command,
			Err:     err,
		}
	}

	printSuccess(options, "INFO  | Completed stage: %s\n", s.Name)
	return nil
}

func selectStages(stages []Stage, options Options) ([]Stage, error) {
	selected := stages

	if options.From != "" {
		start := -1
		for i, stage := range stages {
			if stage.Name == options.From {
				start = i
				break
			}
		}
		if start == -1 {
			return nil, fmt.Errorf("stage %q not found for --from", options.From)
		}
		selected = stages[start:]
	}

	if len(options.Only) > 0 {
		available := make(map[string]struct{}, len(selected))
		for _, stage := range selected {
			available[stage.Name] = struct{}{}
		}

		only := make(map[string]struct{}, len(options.Only))
		for _, name := range options.Only {
			if _, ok := available[name]; !ok {
				return nil, fmt.Errorf("stage %q not found in selected range", name)
			}
			only[name] = struct{}{}
		}

		filtered := make([]Stage, 0, len(only))
		for _, stage := range selected {
			if _, ok := only[stage.Name]; ok {
				filtered = append(filtered, stage)
			}
		}
		selected = filtered
	}

	if len(selected) == 0 {
		return nil, errors.New("no stages selected after applying filters")
	}

	return selected, nil
}

func mergedEnv(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		values[key] = value
	}
	for key, value := range overrides {
		values[key] = value
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, fmt.Sprintf("%s=%s", key, values[key]))
	}
	return env
}

func stageStatus(err error) string {
	if err == nil {
		return "success"
	}

	var sErr *stageError
	if errors.As(err, &sErr) && sErr.Cancelled {
		return "cancelled"
	}

	return "failed"
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func writeSummaryJSON(path string, summary RunSummary) error {
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}

	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create summary directory %q: %w", dir, err)
		}
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write summary file %q: %w", path, err)
	}
	return nil
}

func printInfo(options Options, format string, args ...any) {
	if options.Quiet {
		return
	}
	info.Printf(format, args...)
}

func printSuccess(options Options, format string, args ...any) {
	if options.Quiet {
		return
	}
	success.Printf(format, args...)
}
