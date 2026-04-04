package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fatih/color"
	"gopkg.in/yaml.v3"
)

// PipelineConfig defines the structure of our pipeline.yaml
type PipelineConfig struct {
	Project string  `yaml:"project"`
	Stages  []Stage `yaml:"stages"`
}

type Stage struct {
	Name     string   `yaml:"name"`
	Command  string   `yaml:"command"`
	Args     []string `yaml:"args"`
	Parallel bool     `yaml:"parallel"`
	Timeout  string   `yaml:"timeout,omitempty"`
	Retry    int      `yaml:"retry,omitempty"`
}

var (
	info    = color.New(color.FgCyan).Add(color.Bold)
	success = color.New(color.FgGreen).Add(color.Bold)
	failure = color.New(color.FgRed).Add(color.Bold)
)

type stageError struct {
	Stage     string
	Command   string
	Err       error
	Cancelled bool
}

func (e *stageError) Error() string {
	if e.Cancelled {
		return fmt.Sprintf("stage %q cancelled: %v", e.Stage, e.Err)
	}
	return fmt.Sprintf("stage %q (%s) failed: %v", e.Stage, e.Command, e.Err)
}

func (e *stageError) Unwrap() error {
	return e.Err
}

func main() {
	configPath := flag.String("config", "pipeline.yaml", "Path to pipeline configuration file")
	flag.Parse()

	if err := run(*configPath); err != nil {
		failure.Printf("❌ %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	yamlFile, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("error reading config %q: %w", configPath, err)
	}

	var config PipelineConfig
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		return fmt.Errorf("error parsing %q: %w", configPath, err)
	}

	if err := validateConfig(config); err != nil {
		return err
	}

	info.Printf("🚀 Starting Pipeline: %s\n", config.Project)
	fmt.Println(strings.Repeat("=", 40))
	if err := runPipeline(ctx, config.Stages); err != nil {
		return err
	}

	fmt.Println(strings.Repeat("=", 40))
	success.Println("🎉 PIPELINE SUCCESS: All stages passed!")
	return nil
}

func validateConfig(config PipelineConfig) error {
	if len(config.Stages) == 0 {
		return errors.New("pipeline has no stages")
	}

	names := make(map[string]struct{}, len(config.Stages))
	for i, stage := range config.Stages {
		if strings.TrimSpace(stage.Name) == "" {
			return fmt.Errorf("stage at index %d is missing name", i)
		}
		if strings.TrimSpace(stage.Command) == "" {
			return fmt.Errorf("stage %q is missing command", stage.Name)
		}
		if stage.Retry < 0 {
			return fmt.Errorf("stage %q has invalid retry value %d; must be >= 0", stage.Name, stage.Retry)
		}
		if stage.Timeout != "" {
			timeout, err := time.ParseDuration(stage.Timeout)
			if err != nil {
				return fmt.Errorf("stage %q has invalid timeout %q: %w", stage.Name, stage.Timeout, err)
			}
			if timeout <= 0 {
				return fmt.Errorf("stage %q has non-positive timeout %q", stage.Name, stage.Timeout)
			}
		}
		if _, exists := names[stage.Name]; exists {
			return fmt.Errorf("duplicate stage name %q", stage.Name)
		}
		names[stage.Name] = struct{}{}
	}

	return nil
}

func runPipeline(ctx context.Context, stages []Stage) error {
	var parallelBatch []Stage

	flushParallelBatch := func() error {
		if len(parallelBatch) == 0 {
			return nil
		}

		err := runParallelBatch(ctx, parallelBatch)
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

			if err := executeStageWithRetry(ctx, stage); err != nil {
				return err
			}
		}
	}

	if err := flushParallelBatch(); err != nil {
		return err
	}

	return nil
}

func runParallelBatch(parentCtx context.Context, stages []Stage) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	type stageResult struct {
		err error
	}

	resultCh := make(chan stageResult, len(stages))
	var wg sync.WaitGroup

	for _, stage := range stages {
		wg.Add(1)
		go func(s Stage) {
			defer wg.Done()
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

			err := executeStageWithRetry(ctx, s)
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

func executeStageWithRetry(ctx context.Context, s Stage) error {
	attempts := s.Retry + 1
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		err := executeStage(ctx, s, attempt, attempts)
		if err == nil {
			return nil
		}

		lastErr = err

		var stageErr *stageError
		if errors.As(err, &stageErr) && stageErr.Cancelled {
			return err
		}
		if ctx.Err() != nil {
			return err
		}
		if attempt == attempts {
			break
		}

		info.Printf("🔁 RETRYING: %s (attempt %d/%d)\n", s.Name, attempt+1, attempts)
		if err := sleepWithContext(ctx, time.Second); err != nil {
			return &stageError{
				Stage:     s.Name,
				Command:   s.Command,
				Err:       err,
				Cancelled: true,
			}
		}
	}

	return lastErr
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
func executeStage(ctx context.Context, s Stage, attempt int, totalAttempts int) error {
	runLabel := s.Name
	if totalAttempts > 1 {
		runLabel = fmt.Sprintf("%s (attempt %d/%d)", s.Name, attempt, totalAttempts)
	}

	info.Printf("\n▶️  RUNNING: %s\n", runLabel)
	fmt.Printf("   Command: %s %s\n", s.Command, strings.Join(s.Args, " "))
	if s.Timeout != "" {
		fmt.Printf("   Timeout: %s\n", s.Timeout)
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

	// Stream output to the main terminal
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

	success.Printf("✅ COMPLETED: %s\n", s.Name)
	return nil
}
