package pipeline

import "fmt"

// Config defines the structure of pipeline.yaml.
type Config struct {
	Project     string            `yaml:"project"`
	MaxParallel int               `yaml:"max_parallel,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	Stages      []Stage           `yaml:"stages"`
}

// Stage defines one pipeline stage from configuration.
type Stage struct {
	Name            string            `yaml:"name"`
	Run             string            `yaml:"run,omitempty"`
	Command         string            `yaml:"command,omitempty"`
	Args            []string          `yaml:"args,omitempty"`
	Parallel        bool              `yaml:"parallel"`
	Timeout         string            `yaml:"timeout,omitempty"`
	Retry           int               `yaml:"retry,omitempty"`
	RetryDelay      string            `yaml:"retry_delay,omitempty"`
	Workdir         string            `yaml:"workdir,omitempty"`
	Env             map[string]string `yaml:"env,omitempty"`
	ContinueOnError bool              `yaml:"continue_on_error,omitempty"`
}

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
