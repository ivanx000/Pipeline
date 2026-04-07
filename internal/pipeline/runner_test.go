package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func requirePOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test requires POSIX shell")
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name: "valid config",
			config: Config{
				Project:     "ok",
				MaxParallel: 2,
				Stages: []Stage{{
					Name:    "build",
					Command: "go",
					Args:    []string{"version"},
					Env:     map[string]string{"GOFLAGS": "-mod=mod"},
				}},
			},
		},
		{
			name:    "negative max parallel",
			config:  Config{Project: "invalid", MaxParallel: -1, Stages: []Stage{{Name: "build", Command: "go"}}},
			wantErr: "max_parallel has invalid value",
		},
		{
			name:    "no stages",
			config:  Config{Project: "empty"},
			wantErr: "pipeline has no stages",
		},
		{
			name: "missing stage name",
			config: Config{
				Stages: []Stage{{Command: "echo"}},
			},
			wantErr: "missing name",
		},
		{
			name: "missing command",
			config: Config{
				Stages: []Stage{{Name: "lint"}},
			},
			wantErr: "missing command",
		},
		{
			name: "duplicate stage name",
			config: Config{
				Stages: []Stage{{Name: "lint", Command: "echo"}, {Name: "lint", Command: "echo"}},
			},
			wantErr: "duplicate stage name",
		},
		{
			name: "negative retry",
			config: Config{
				Stages: []Stage{{Name: "lint", Command: "echo", Retry: -1}},
			},
			wantErr: "invalid retry value",
		},
		{
			name: "invalid timeout format",
			config: Config{
				Stages: []Stage{{Name: "lint", Command: "echo", Timeout: "soon"}},
			},
			wantErr: "invalid timeout",
		},
		{
			name: "non positive timeout",
			config: Config{
				Stages: []Stage{{Name: "lint", Command: "echo", Timeout: "0s"}},
			},
			wantErr: "non-positive timeout",
		},
		{
			name: "invalid env key",
			config: Config{
				Stages: []Stage{{Name: "lint", Command: "echo", Env: map[string]string{"": "value"}}},
			},
			wantErr: "env entry with empty key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.config)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("validateConfig() unexpected error: %v", err)
			}
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("validateConfig() expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateConfig() error %q does not contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestExecuteStageWithRetrySucceedsOnRetry(t *testing.T) {
	requirePOSIXShell(t)

	flagFile := filepath.Join(t.TempDir(), "retry-once.flag")
	script := fmt.Sprintf(`if [ ! -f %q ]; then touch %q; echo fail-first; exit 1; fi; echo pass-second`, flagFile, flagFile)

	_, err := executeStageWithRetry(context.Background(), Stage{
		Name:    "retry-once",
		Command: "sh",
		Args:    []string{"-c", script},
		Retry:   1,
	}, Options{})
	if err != nil {
		t.Fatalf("executeStageWithRetry() unexpected error: %v", err)
	}

	if _, statErr := os.Stat(flagFile); statErr != nil {
		t.Fatalf("expected retry marker file to exist: %v", statErr)
	}
}

func TestExecuteStageWithRetryTimesOut(t *testing.T) {
	requirePOSIXShell(t)

	_, err := executeStageWithRetry(context.Background(), Stage{
		Name:    "timeout",
		Command: "sh",
		Args:    []string{"-c", "sleep 1"},
		Timeout: "100ms",
		Retry:   0,
	}, Options{})
	if err == nil {
		t.Fatal("executeStageWithRetry() expected timeout error, got nil")
	}

	var sErr *stageError
	if !errors.As(err, &sErr) {
		t.Fatalf("expected stageError, got %T", err)
	}
	if !strings.Contains(sErr.Error(), "timed out") {
		t.Fatalf("expected timeout message, got: %v", sErr)
	}
}

func TestRunPipelineStopsBeforeSequentialAfterParallelFailure(t *testing.T) {
	requirePOSIXShell(t)

	marker := filepath.Join(t.TempDir(), "sequential-ran.flag")
	stages := []Stage{
		{Name: "parallel-fail", Command: "sh", Args: []string{"-c", "exit 1"}, Parallel: true},
		{Name: "parallel-slow", Command: "sh", Args: []string{"-c", "sleep 1"}, Parallel: true},
		{Name: "sequential-should-not-run", Command: "sh", Args: []string{"-c", fmt.Sprintf("touch %q", marker)}},
	}

	err := runPipeline(context.Background(), stages, 0, Options{}, nil)
	if err == nil {
		t.Fatal("runPipeline() expected error, got nil")
	}

	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected sequential stage not to run; marker state err=%v", statErr)
	}
}

func TestExecuteStageWithWorkdirAndEnv(t *testing.T) {
	requirePOSIXShell(t)

	tempDir := t.TempDir()
	errFile := filepath.Join(tempDir, "pwd.txt")

	_, err := executeStageWithRetry(context.Background(), Stage{
		Name:    "workdir-env",
		Command: "sh",
		Args:    []string{"-c", fmt.Sprintf("[ \"$PIPELINE_ENV_TEST\" = \"ok\" ] && pwd > %q", errFile)},
		Workdir: tempDir,
		Env: map[string]string{
			"PIPELINE_ENV_TEST": "ok",
		},
	}, Options{Quiet: true})
	if err != nil {
		t.Fatalf("executeStageWithRetry() unexpected error: %v", err)
	}

	raw, readErr := os.ReadFile(errFile)
	if readErr != nil {
		t.Fatalf("expected pwd output file: %v", readErr)
	}

	got := strings.TrimSpace(string(raw))
	resolvedTempDir, err := filepath.EvalSymlinks(tempDir)
	if err != nil {
		t.Fatalf("failed to resolve temp dir symlink: %v", err)
	}
	resolvedGot, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("failed to resolve pwd symlink: %v", err)
	}
	if resolvedGot != resolvedTempDir {
		t.Fatalf("expected workdir %q, got %q", resolvedTempDir, resolvedGot)
	}
}

func TestSelectStagesWithFromAndOnly(t *testing.T) {
	stages := []Stage{
		{Name: "lint", Command: "echo"},
		{Name: "test", Command: "echo"},
		{Name: "build", Command: "echo"},
	}

	selected, err := selectStages(stages, Options{From: "test", Only: []string{"build", "test"}})
	if err != nil {
		t.Fatalf("selectStages() unexpected error: %v", err)
	}

	if len(selected) != 2 {
		t.Fatalf("expected 2 selected stages, got %d", len(selected))
	}
	if selected[0].Name != "test" || selected[1].Name != "build" {
		t.Fatalf("unexpected selected stage order: %v, %v", selected[0].Name, selected[1].Name)
	}
}

func TestRunPipelineWithMaxParallelOne(t *testing.T) {
	requirePOSIXShell(t)

	stages := []Stage{
		{Name: "p1", Command: "sh", Args: []string{"-c", "sleep 0.2"}, Parallel: true},
		{Name: "p2", Command: "sh", Args: []string{"-c", "sleep 0.2"}, Parallel: true},
	}

	start := time.Now()
	err := runPipeline(context.Background(), stages, 1, Options{Quiet: true}, nil)
	if err != nil {
		t.Fatalf("runPipeline() unexpected error: %v", err)
	}

	if elapsed := time.Since(start); elapsed < 350*time.Millisecond {
		t.Fatalf("expected max_parallel=1 to serialize parallel stages, elapsed=%v", elapsed)
	}
}
