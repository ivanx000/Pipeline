# Pipeline

Pipeline is a lightweight CI/CD pipeline runner written in Go.

It reads a local YAML configuration file, executes stages as shell commands, and supports controlled parallel execution, retries, stage-level timeouts, and signal-aware cancellation.

The project is designed as a single-binary CLI with a clean internal package layout.

## What It Does

- Loads pipeline configuration from YAML
- Validates configuration before execution
- Runs stages sequentially by default
- Runs adjacent stages concurrently when `parallel: true`
- Ensures sequential stages wait for active parallel batches
- Fails fast on stage failure and cancels in-flight sibling stages
- Supports retry and timeout per stage
- Handles Ctrl+C / SIGTERM gracefully
- Provides professional CLI logs with optional quiet mode

## Key Features

- Simple config-first workflow
- Strong execution guarantees around stage ordering
- Idiomatic Go concurrency with cancellation propagation
- Minimal dependencies
- Easy to extend

## Project Structure

```text
.
|-- main.go                      # CLI entrypoint and flag parsing
|-- pipeline.yaml                # Example pipeline config
|-- internal/
|   `-- pipeline/
|       |-- types.go             # Config and stage models
|       |-- config.go            # YAML load and validation
|       |-- runner.go            # Orchestration, retries, timeouts, logging
|       `-- runner_test.go       # Unit and behavior tests
|-- go.mod
`-- go.sum
```

## Requirements

- Go 1.26.1 or newer (project currently targets 1.26.1)

## Build

```bash
go build -o gopipe .
```

## Run

Default config path:

```bash
go run .
```

Custom config path:

```bash
go run . --config pipeline.yaml
```

Quiet mode (suppresses Pipeline info logs, keeps warnings/errors):

```bash
go run . --config pipeline.yaml --quiet
```

Built binary:

```bash
./gopipe --config pipeline.yaml
```

## CLI Flags

- `--config string`
  - Path to pipeline configuration file
  - Default: `pipeline.yaml`
- `--quiet`
  - Suppress informational Pipeline logs
  - Warnings and errors still print
  - Stage command output is still streamed

## Configuration File

Pipeline reads a YAML document with this structure:

```yaml
project: "My-Go-Pipeline"
stages:
  - name: "Linting"
    command: "go"
    args: ["vet", "./..."]
    parallel: true
    timeout: "30s"
    retry: 1
```

### Top-level fields

- `project` (string): Display name for the run
- `stages` (array): Ordered list of stages to execute

### Stage fields

- `name` (string, required): Human-readable stage name
- `command` (string, required): Executable to run
- `args` (string array, optional): Command arguments
- `parallel` (bool, optional): If true, stage may run with adjacent parallel stages
- `timeout` (duration string, optional): Per-attempt timeout (Go duration format, for example `500ms`, `30s`, `2m`)
- `retry` (int, optional): Number of retries after first failed attempt (`retry: 2` means 3 total attempts)

## Execution Model

Pipeline processes stages in definition order.

- Sequential stages (`parallel: false` or omitted)
  - Run one at a time
- Parallel batches (`parallel: true`)
  - Consecutive parallel stages form a batch
  - Stages in the batch run concurrently

Barrier behavior:

- A sequential stage will not start until all currently running parallel stages are finished (or canceled)
- Final completion waits for any remaining parallel batch

Failure behavior:

- If one stage in a parallel batch fails, sibling stages are canceled (fail-fast)
- The pipeline exits with a non-zero status

## Validation Rules

Before execution, Pipeline validates config and fails early on:

- No stages provided
- Missing stage name
- Missing command
- Duplicate stage names
- Negative retry values
- Invalid timeout format
- Non-positive timeout values

## Logging Behavior

Pipeline uses structured console prefixes:

- `INFO  | ...`
- `WARN  | ...`
- `ERROR | ...`

When `--quiet` is enabled:

- Pipeline `INFO` logs are suppressed
- `WARN` and `ERROR` logs remain
- Subprocess stdout/stderr still streams

## Retries and Timeouts

Retries are attempt-based and timeout applies per attempt.

Example:

```yaml
- name: "Flaky API Check"
  command: "sh"
  args: ["-c", "./scripts/check-api.sh"]
  retry: 2
  timeout: "10s"
```

Behavior:

- Attempt 1 runs with a 10s timeout
- On failure, stage is retried up to 2 additional times
- If all attempts fail, pipeline fails

## Signal Handling

Pipeline listens for interrupt/termination signals and propagates cancellation through context.

- Ctrl+C cancels running stages
- Active commands are terminated via context-aware execution

## Testing

Run all tests:

```bash
go test ./...
```

Run with race detector:

```bash
go test -race ./...
```

## Example pipeline.yaml

The repository includes a working example in `pipeline.yaml`:

- Linting and Security Check run in parallel
- Build Binary runs only after parallel stages complete

## Typical Workflow

1. Edit `pipeline.yaml`
2. Run `go run . --config pipeline.yaml`
3. Iterate on stages as needed
4. Build binary with `go build -o gopipe .` for local distribution

## Extensibility Notes

The internal package layout is intentionally split for maintainability:

- `types.go` for models
- `config.go` for parsing and validation
- `runner.go` for orchestration and runtime behavior

This makes future features straightforward, such as:

- Stage dependencies (`depends_on`)
- Conditional execution
- Environment variable injection per stage
- Artifact or output capture
- Alternative executors

## License

Add your preferred license here.
