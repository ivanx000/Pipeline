package pipeline

import internalpipeline "github.com/ivanx000/Pipeline/internal/pipeline"

// Options controls runtime behavior for pipeline execution.
type Options = internalpipeline.Options

// StageSummary stores one stage execution result for JSON reporting.
type StageSummary = internalpipeline.StageSummary

// RunSummary stores a whole pipeline execution result for JSON reporting.
type RunSummary = internalpipeline.RunSummary

// Run executes the pipeline from the provided config path.
func Run(configPath string) error {
	return internalpipeline.Run(configPath)
}

// RunWithOptions executes the pipeline from the provided config path using runtime options.
func RunWithOptions(configPath string, options Options) error {
	return internalpipeline.RunWithOptions(configPath, options)
}
