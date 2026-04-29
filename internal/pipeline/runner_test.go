package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
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
			wantErr: "must have either",
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
		{
			name: "invalid retry_delay format",
			config: Config{
				Stages: []Stage{{Name: "lint", Command: "echo", Retry: 1, RetryDelay: "fast"}},
			},
			wantErr: "invalid retry_delay",
		},
		{
			name: "zero retry_delay",
			config: Config{
				Stages: []Stage{{Name: "lint", Command: "echo", Retry: 1, RetryDelay: "0s"}},
			},
			wantErr: "non-positive retry_delay",
		},
		{
			name: "valid retry_delay",
			config: Config{
				Stages: []Stage{{Name: "lint", Command: "echo", Retry: 1, RetryDelay: "500ms"}},
			},
		},
		{
			name: "both run and command set",
			config: Config{
				Stages: []Stage{{Name: "lint", Command: "echo", Run: "echo hello"}},
			},
			wantErr: "both 'command' and 'run'",
		},
		{
			name: "valid run only",
			config: Config{
				Stages: []Stage{{Name: "lint", Run: "echo hello"}},
			},
		},
		{
			name: "pipeline env empty key",
			config: Config{
				Env:    map[string]string{"": "value"},
				Stages: []Stage{{Name: "lint", Command: "echo"}},
			},
			wantErr: "pipeline env has entry with empty key",
		},
		{
			name: "pipeline env key with equals",
			config: Config{
				Env:    map[string]string{"A=B": "value"},
				Stages: []Stage{{Name: "lint", Command: "echo"}},
			},
			wantErr: "pipeline env has invalid key",
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
	}, Options{}, nil)
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
	}, Options{}, nil)
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

	err := runPipeline(context.Background(), stages, 0, Options{}, nil, nil)
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
	}, Options{Quiet: true}, nil)
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
	err := runPipeline(context.Background(), stages, 1, Options{Quiet: true}, nil, nil)
	if err != nil {
		t.Fatalf("runPipeline() unexpected error: %v", err)
	}

	if elapsed := time.Since(start); elapsed < 350*time.Millisecond {
		t.Fatalf("expected max_parallel=1 to serialize parallel stages, elapsed=%v", elapsed)
	}
}

func TestExecuteStageWithRetryDryRunSkipsCommandExecution(t *testing.T) {
	requirePOSIXShell(t)

	marker := filepath.Join(t.TempDir(), "dry-run.marker")
	_, err := executeStageWithRetry(context.Background(), Stage{
		Name:    "dry-run-skip",
		Command: "sh",
		Args:    []string{"-c", fmt.Sprintf("touch %q", marker)},
	}, Options{DryRun: true, Quiet: true}, nil)
	if err != nil {
		t.Fatalf("executeStageWithRetry() unexpected error in dry-run: %v", err)
	}

	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected marker not to be created in dry-run; statErr=%v", statErr)
	}
}

func TestRunWritesSummaryJSONInDryRun(t *testing.T) {
	requirePOSIXShell(t)

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "pipeline.yaml")
	summaryPath := filepath.Join(tempDir, "summary.json")
	marker := filepath.Join(tempDir, "should-not-exist.marker")

	config := fmt.Sprintf(`project: "dry-run-summary"
stages:
  - name: "touch-skip"
    command: "sh"
    args: ["-c", "touch %s"]
`, marker)
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	err := run(context.Background(), configPath, Options{
		Quiet:           true,
		DryRun:          true,
		SummaryJSONPath: summaryPath,
	})
	if err != nil {
		t.Fatalf("run() unexpected error in dry-run: %v", err)
	}

	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected marker not to be created in dry-run; statErr=%v", statErr)
	}

	raw, readErr := os.ReadFile(summaryPath)
	if readErr != nil {
		t.Fatalf("expected summary JSON to be written: %v", readErr)
	}

	var summary RunSummary
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatalf("invalid summary JSON: %v", err)
	}

	if summary.Status != "success" {
		t.Fatalf("expected summary status success, got %q", summary.Status)
	}
	if len(summary.Stages) != 1 {
		t.Fatalf("expected 1 stage summary, got %d", len(summary.Stages))
	}
	if summary.Stages[0].Status != "success" {
		t.Fatalf("expected stage status success, got %q", summary.Stages[0].Status)
	}
	if summary.Stages[0].Attempts != 1 {
		t.Fatalf("expected stage attempts 1 in dry-run, got %d", summary.Stages[0].Attempts)
	}
}

func TestPreflightCommandsReturnsAllMissingCommands(t *testing.T) {
	stages := []Stage{
		{Name: "missing-a", Command: "pipeline-missing-cmd-abc123"},
		{Name: "missing-b", Command: "pipeline-missing-cmd-abc123"},
		{Name: "missing-c", Command: "pipeline-missing-cmd-def456"},
	}

	err := preflightCommands(stages)
	if err == nil {
		t.Fatal("preflightCommands() expected error for missing commands")
	}

	msg := err.Error()
	if !strings.Contains(msg, "pipeline-missing-cmd-abc123") || !strings.Contains(msg, "pipeline-missing-cmd-def456") {
		t.Fatalf("expected both missing command names in error, got: %q", msg)
	}
	if !strings.Contains(msg, "missing-a") || !strings.Contains(msg, "missing-b") || !strings.Contains(msg, "missing-c") {
		t.Fatalf("expected related stage names in error, got: %q", msg)
	}
}

func TestRunFailsPreflightBeforeAnyStageExecutes(t *testing.T) {
	requirePOSIXShell(t)

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "pipeline.yaml")
	marker := filepath.Join(tempDir, "must-not-exist.marker")

	config := fmt.Sprintf(`project: "preflight-fail"
stages:
  - name: "touch-marker"
    command: "sh"
    args: ["-c", "touch %s"]
  - name: "missing-cmd"
    command: "pipeline-missing-cmd-xyz999"
`, marker)
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	err := run(context.Background(), configPath, Options{Quiet: true})
	if err == nil {
		t.Fatal("run() expected preflight error, got nil")
	}
	if !strings.Contains(err.Error(), "preflight failed") {
		t.Fatalf("expected preflight error, got: %v", err)
	}

	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected marker not to be created when preflight fails; statErr=%v", statErr)
	}
}

func TestRunSkipPreflightBypassesChecks(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "pipeline.yaml")

	config := `project: "skip-preflight"
stages:
  - name: "missing-cmd"
    command: "pipeline-missing-cmd-jkl123"
`
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	err := run(context.Background(), configPath, Options{Quiet: true, SkipPreflight: true})
	if err == nil {
		t.Fatal("run() expected runtime command error, got nil")
	}
	if strings.Contains(err.Error(), "preflight failed") {
		t.Fatalf("expected non-preflight error when SkipPreflight is enabled, got: %v", err)
	}
}

func TestExecuteStageWritesToInjectedStdout(t *testing.T) {
	requirePOSIXShell(t)

	var buf bytes.Buffer
	_, err := executeStageWithRetry(context.Background(), Stage{
		Name: "stdout-inject",
		Run:  "echo hello-injected",
	}, Options{Quiet: true, Stdout: &buf}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "hello-injected") {
		t.Fatalf("expected stdout buffer to contain %q, got %q", "hello-injected", buf.String())
	}
}

func TestExecuteStageWritesToInjectedStderr(t *testing.T) {
	requirePOSIXShell(t)

	var buf bytes.Buffer
	_, err := executeStageWithRetry(context.Background(), Stage{
		Name: "stderr-inject",
		Run:  "echo err-injected >&2",
	}, Options{Quiet: true, Stderr: &buf}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "err-injected") {
		t.Fatalf("expected stderr buffer to contain %q, got %q", "err-injected", buf.String())
	}
}

func TestExecuteStageWithRetryUsesRetryDelay(t *testing.T) {
	requirePOSIXShell(t)

	flagFile := filepath.Join(t.TempDir(), "delay-test.flag")
	script := fmt.Sprintf(`if [ ! -f %q ]; then touch %q; exit 1; fi`, flagFile, flagFile)

	start := time.Now()
	_, err := executeStageWithRetry(context.Background(), Stage{
		Name:       "delay-test",
		Command:    "sh",
		Args:       []string{"-c", script},
		Retry:      1,
		RetryDelay: "150ms",
	}, Options{Quiet: true}, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed < 150*time.Millisecond {
		t.Fatalf("expected retry delay >= 150ms, got %v", elapsed)
	}
	if elapsed > 900*time.Millisecond {
		t.Fatalf("expected retry delay well under 1s default, got %v", elapsed)
	}
}

func TestExecuteStageRunShorthand(t *testing.T) {
	requirePOSIXShell(t)

	var buf bytes.Buffer
	_, err := executeStageWithRetry(context.Background(), Stage{
		Name: "run-shorthand",
		Run:  "echo run-shorthand-ok",
	}, Options{Quiet: true, Stdout: &buf}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "run-shorthand-ok") {
		t.Fatalf("expected output %q, got %q", "run-shorthand-ok", buf.String())
	}
}

func TestExecuteStageRunShorthandWithPipeAndRedirect(t *testing.T) {
	requirePOSIXShell(t)

	var buf bytes.Buffer
	_, err := executeStageWithRetry(context.Background(), Stage{
		Name: "run-pipe",
		Run:  "echo foo | tr a-z A-Z",
	}, Options{Quiet: true, Stdout: &buf}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "FOO") {
		t.Fatalf("expected piped output %q, got %q", "FOO", buf.String())
	}
}

func TestContinueOnErrorAllowsPipelineToContinue(t *testing.T) {
	requirePOSIXShell(t)

	marker := filepath.Join(t.TempDir(), "continue-ran.flag")
	stages := []Stage{
		{Name: "fail-but-continue", Command: "sh", Args: []string{"-c", "exit 1"}, ContinueOnError: true},
		{Name: "should-run", Command: "sh", Args: []string{"-c", fmt.Sprintf("touch %q", marker)}},
	}

	err := runPipeline(context.Background(), stages, 0, Options{Quiet: true}, nil, nil)
	if err != nil {
		t.Fatalf("expected pipeline to succeed with continue_on_error, got: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("expected subsequent stage to run after continue_on_error: %v", statErr)
	}
}

func TestContinueOnErrorRecordsFailedStatus(t *testing.T) {
	requirePOSIXShell(t)

	recorder := newSummaryRecorder("test", "pipeline.yaml", 0, 1)
	err := executeStageWithRetryTracked(context.Background(), Stage{
		Name:            "fail-recorded",
		Command:         "sh",
		Args:            []string{"-c", "exit 1"},
		ContinueOnError: true,
	}, Options{Quiet: true}, recorder, nil)

	if err != nil {
		t.Fatalf("expected nil error with continue_on_error, got: %v", err)
	}
	snap := recorder.snapshot()
	if len(snap.Stages) != 1 {
		t.Fatalf("expected 1 stage summary, got %d", len(snap.Stages))
	}
	if snap.Stages[0].Status != "failed" {
		t.Fatalf("expected recorded status %q, got %q", "failed", snap.Stages[0].Status)
	}
}

func TestPipelineEnvIsPassedToStage(t *testing.T) {
	requirePOSIXShell(t)

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "pipeline.yaml")
	config := `project: "env-test"
env:
  PIPELINE_TEST_VAR: "pipeline_value"
stages:
  - name: "check-env"
    run: 'test "$PIPELINE_TEST_VAR" = "pipeline_value"'
`
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	if err := run(context.Background(), configPath, Options{Quiet: true}); err != nil {
		t.Fatalf("expected pipeline env to be visible in stage, got: %v", err)
	}
}

func TestStageEnvOverridesPipelineEnv(t *testing.T) {
	requirePOSIXShell(t)

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "pipeline.yaml")
	config := `project: "env-override-test"
env:
  SHARED_VAR: "pipeline"
stages:
  - name: "check-override"
    run: 'test "$SHARED_VAR" = "stage"'
    env:
      SHARED_VAR: "stage"
`
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	if err := run(context.Background(), configPath, Options{Quiet: true}); err != nil {
		t.Fatalf("expected stage env to override pipeline env, got: %v", err)
	}
}

func TestMergedEnvThreeLayers(t *testing.T) {
	base := []string{"KEY=base", "BASE_ONLY=yes"}
	pipelineEnv := map[string]string{"KEY": "pipeline", "PIPE_ONLY": "yes"}
	stageEnv := map[string]string{"KEY": "stage"}

	result := mergedEnv(base, pipelineEnv, stageEnv)

	resultMap := make(map[string]string, len(result))
	for _, entry := range result {
		k, v, _ := strings.Cut(entry, "=")
		resultMap[k] = v
	}

	if resultMap["KEY"] != "stage" {
		t.Fatalf("expected KEY=stage (stage wins), got %q", resultMap["KEY"])
	}
	if resultMap["PIPE_ONLY"] != "yes" {
		t.Fatalf("expected PIPE_ONLY=yes from pipeline env, got %q", resultMap["PIPE_ONLY"])
	}
	if resultMap["BASE_ONLY"] != "yes" {
		t.Fatalf("expected BASE_ONLY=yes from base env, got %q", resultMap["BASE_ONLY"])
	}
}
