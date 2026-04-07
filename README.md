# Pipeline

Pipeline is a lightweight local CI runner written in Go.

It executes YAML-defined stages with retries, timeouts, cancellation handling, scoped parallelism, stage filters, stage-level environment overrides, and JSON run summaries.

## Why Use It

- Keep local automation simple and reproducible
- Run the same checks before pushing code
- Fail fast with clear logs
- Keep config in version control

## Quick Start For Local Projects

This section walks through setting up Pipeline in any local repository.

1. Build the CLI once

```bash
git clone https://github.com/ivanx000/Pipeline.git
cd Pipeline
go build -o gopipe .
```

2. Copy `gopipe` somewhere on your PATH or invoke it with an absolute path

```bash
mv gopipe /usr/local/bin/gopipe
```

3. In your target project, create `pipeline.yaml`

```yaml
project: "my-local-project"
max_parallel: 2

stages:
  - name: "Lint"
    command: "go"
    args: ["vet", "./..."]
    parallel: true

  - name: "Unit Tests"
    command: "go"
    args: ["test", "./..."]
    parallel: true
    timeout: "2m"
    retry: 1

  - name: "Build"
    command: "go"
    args: ["build", "./..."]
```

4. Run Pipeline from the project root

```bash
gopipe --config pipeline.yaml
```

5. Optional: save a machine-readable summary for tooling

```bash
gopipe --config pipeline.yaml --summary-json .pipeline/last-run.json
```

## CLI Usage

Run with defaults:

```bash
go run .
```

Run against a custom config:

```bash
go run . --config path/to/pipeline.yaml
```

Run a subset from a stage onward:

```bash
go run . --from "Unit Tests"
```

Run only named stages:

```bash
go run . --only "Lint,Unit Tests"
```

Override max parallelism from CLI:

```bash
go run . --max-parallel 1
```

Quiet mode:

```bash
go run . --quiet
```

## CLI Flags

- `--config string`
  - Path to config file
  - Default: `pipeline.yaml`
- `--quiet`
  - Suppress Pipeline `INFO` logs
  - Keep `WARN` and `ERROR` logs
  - Keep subprocess stdout/stderr streaming
- `--from string`
  - Start execution from the named stage (inclusive)
- `--only string`
  - Comma-separated stage names to run
  - Preserves original stage order
- `--max-parallel int`
  - Override config `max_parallel`
  - `0` means use config value
- `--summary-json string`
  - Write a JSON run summary to file

## Configuration Reference

Top-level fields:

- `project` (string): Display name for the run
- `max_parallel` (int, optional): Max concurrent stages inside each adjacent parallel batch. `0` means unlimited
- `stages` (array): Ordered stage list

Stage fields:

- `name` (string, required): Unique stage name
- `command` (string, required): Executable to run
- `args` (array of strings, optional): Command arguments
- `parallel` (bool, optional): If true, can run in the current parallel batch
- `timeout` (duration string, optional): Per-attempt timeout (for example `500ms`, `30s`, `2m`)
- `retry` (int, optional): Retries after first failure
- `workdir` (string, optional): Working directory for command execution
- `env` (map of string to string, optional): Environment variable overrides for that stage

## Full Example

```yaml
project: "polyglot-monorepo"
max_parallel: 2

stages:
  - name: "Go Lint"
    command: "go"
    args: ["vet", "./..."]
    parallel: true

  - name: "Frontend Lint"
    command: "npm"
    args: ["run", "lint"]
    workdir: "web"
    env:
      NODE_ENV: "test"
    parallel: true

  - name: "Go Tests"
    command: "go"
    args: ["test", "./..."]
    timeout: "3m"
    retry: 1

  - name: "Build Web"
    command: "npm"
    args: ["run", "build"]
    workdir: "web"
```

## Execution Model

- Stages are evaluated in file order
- Adjacent `parallel: true` stages form one parallel batch
- Sequential stages wait until the active parallel batch completes
- Any non-cancelled failure in a parallel batch cancels sibling stages (fail-fast)
- Retries are per stage attempt
- Timeout applies per attempt

## JSON Summary Output

When `--summary-json` is provided, Pipeline writes a file like:

```json
{
  "project": "my-local-project",
  "config_path": "pipeline.yaml",
  "status": "success",
  "started_at": "2026-04-07T10:00:00Z",
  "finished_at": "2026-04-07T10:00:03Z",
  "duration_ms": 3000,
  "max_parallel": 2,
  "stage_count": 3,
  "stages": [
    {
      "name": "Lint",
      "command": "go vet ./...",
      "status": "success",
      "attempts": 1,
      "duration_ms": 420
    }
  ]
}
```

Per-stage status values are:

- `success`
- `failed`
- `cancelled`

## Validation Rules

Pipeline fails early when config is invalid, including:

- Missing stages
- Missing stage name or command
- Duplicate stage names
- Negative retry values
- Invalid/non-positive timeout values
- Negative `max_parallel`
- Invalid env keys (empty or containing `=`)

## Use As A Go Package

If you want to embed Pipeline in a Go app, import:

```go
import "github.com/ivanx000/Pipeline/pkg/pipeline"
```

And execute:

```go
err := pipeline.RunWithOptions("pipeline.yaml", pipeline.Options{
    Quiet:           false,
    From:            "Lint",
    Only:            []string{"Lint", "Unit Tests"},
    MaxParallel:     2,
    SummaryJSONPath: "run-summary.json",
})
```

## Local Development

Run tests:

```bash
go test ./...
```

Run race detector:

```bash
go test -race ./...
```

## Project Layout

```text
.
|-- main.go
|-- pipeline.yaml
|-- pkg/
|   `-- pipeline/
|       `-- pipeline.go
|-- internal/
|   `-- pipeline/
|       |-- types.go
|       |-- config.go
|       |-- runner.go
|       `-- runner_test.go
|-- go.mod
`-- go.sum
```

## License

Add your preferred license here.
