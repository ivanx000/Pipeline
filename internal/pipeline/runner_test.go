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
				Project: "ok",
				Stages:  []Stage{{Name: "build", Command: "go", Args: []string{"version"}}},
			},
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

	err := executeStageWithRetry(context.Background(), Stage{
		Name:    "retry-once",
		Command: "sh",
		Args:    []string{"-c", script},
		Retry:   1,
	})
	if err != nil {
		t.Fatalf("executeStageWithRetry() unexpected error: %v", err)
	}

	if _, statErr := os.Stat(flagFile); statErr != nil {
		t.Fatalf("expected retry marker file to exist: %v", statErr)
	}
}

func TestExecuteStageWithRetryTimesOut(t *testing.T) {
	requirePOSIXShell(t)

	err := executeStageWithRetry(context.Background(), Stage{
		Name:    "timeout",
		Command: "sh",
		Args:    []string{"-c", "sleep 1"},
		Timeout: "100ms",
		Retry:   0,
	})
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

	err := runPipeline(context.Background(), stages)
	if err == nil {
		t.Fatal("runPipeline() expected error, got nil")
	}

	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected sequential stage not to run; marker state err=%v", statErr)
	}
}
