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
	success = color.New(color.FgGreen).Add(color.Bold)
)

// Run executes the pipeline from the provided config path.
func Run(configPath string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return run(ctx, configPath)
}

func run(ctx context.Context, configPath string) error {
	config, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	info.Printf("INFO  | Starting pipeline: %s\n", config.Project)
	fmt.Println(strings.Repeat("=", 40))
	if err := runPipeline(ctx, config.Stages); err != nil {
		return err
	}

	fmt.Println(strings.Repeat("=", 40))
	success.Println("INFO  | Pipeline completed successfully.")
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

		info.Printf("WARN  | Retrying stage %q (attempt %d/%d)\n", s.Name, attempt+1, attempts)
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

	info.Printf("\nINFO  | Running stage: %s\n", runLabel)
	fmt.Printf("INFO  | Command: %s %s\n", s.Command, strings.Join(s.Args, " "))
	if s.Timeout != "" {
		fmt.Printf("INFO  | Timeout: %s\n", s.Timeout)
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

	success.Printf("INFO  | Completed stage: %s\n", s.Name)
	return nil
}
