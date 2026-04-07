package pipeline

import "fmt"

// Config defines the structure of pipeline.yaml.
type Config struct {
	Project     string  `yaml:"project"`
	MaxParallel int     `yaml:"max_parallel,omitempty"`
	Stages      []Stage `yaml:"stages"`
}

// Stage defines one pipeline stage from configuration.
type Stage struct {
	Name     string            `yaml:"name"`
	Command  string            `yaml:"command"`
	Args     []string          `yaml:"args"`
	Parallel bool              `yaml:"parallel"`
	Timeout  string            `yaml:"timeout,omitempty"`
	Retry    int               `yaml:"retry,omitempty"`
	Workdir  string            `yaml:"workdir,omitempty"`
	Env      map[string]string `yaml:"env,omitempty"`
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
