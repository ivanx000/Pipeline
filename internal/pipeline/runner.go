package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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
	Quiet bool
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

	printInfo(options, "INFO  | Starting pipeline: %s\n", config.Project)
	if !options.Quiet {
		fmt.Println(strings.Repeat("=", 40))
	}
	if err := runPipeline(ctx, config.Stages, options); err != nil {
		return err
	}

	if !options.Quiet {
		fmt.Println(strings.Repeat("=", 40))
	}
	printSuccess(options, "INFO  | Pipeline completed successfully.\n")
	return nil
}

func runPipeline(ctx context.Context, stages []Stage, options Options) error {
	var parallelBatch []Stage

	flushParallelBatch := func() error {
		if len(parallelBatch) == 0 {
			return nil
		}

		err := runParallelBatch(ctx, parallelBatch, options)
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

			if err := executeStageWithRetry(ctx, stage, options); err != nil {
				return err
			}
		}
	}

	if err := flushParallelBatch(); err != nil {
		return err
	}

	return nil
}

func runParallelBatch(parentCtx context.Context, stages []Stage, options Options) error {
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

			err := executeStageWithRetry(ctx, s, options)
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

func executeStageWithRetry(ctx context.Context, s Stage, options Options) error {
	attempts := s.Retry + 1
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		err := executeStage(ctx, s, attempt, attempts, options)
		if err == nil {
			return nil
		}

		lastErr = err

		var sErr *stageError
		if errors.As(err, &sErr) && sErr.Cancelled {
			return err
		}
		if ctx.Err() != nil {
			return err
		}
		if attempt == attempts {
			break
		}

		warn.Printf("WARN  | Retrying stage %q (attempt %d/%d)\n", s.Name, attempt+1, attempts)
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
func executeStage(ctx context.Context, s Stage, attempt int, totalAttempts int, options Options) error {
	runLabel := s.Name
	if totalAttempts > 1 {
		runLabel = fmt.Sprintf("%s (attempt %d/%d)", s.Name, attempt, totalAttempts)
	}

	printInfo(options, "\nINFO  | Running stage: %s\n", runLabel)
	printInfo(options, "INFO  | Command: %s %s\n", s.Command, strings.Join(s.Args, " "))
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
