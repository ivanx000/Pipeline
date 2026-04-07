package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ivanx000/Pipeline/pkg/pipeline"
)

func main() {
	configPath := flag.String("config", "pipeline.yaml", "Path to pipeline configuration file")
	quiet := flag.Bool("quiet", false, "Suppress informational logs and only show warnings/errors")
	from := flag.String("from", "", "Run from this stage name onward")
	only := flag.String("only", "", "Comma-separated list of stage names to run")
	summaryJSON := flag.String("summary-json", "", "Write machine-readable run summary to this JSON file")
	maxParallel := flag.Int("max-parallel", 0, "Override config max parallel stage count (0 uses config value)")
	dryRun := flag.Bool("dry-run", false, "Preview planned execution without running stage commands")
	skipPreflight := flag.Bool("skip-preflight", false, "Skip preflight command availability checks")
	flag.Parse()

	if *maxParallel < 0 {
		fmt.Fprintln(os.Stderr, "ERROR | --max-parallel must be >= 0")
		os.Exit(1)
	}

	onlyStages := parseCSV(*only)

	if err := pipeline.RunWithOptions(*configPath, pipeline.Options{
		Quiet:           *quiet,
		From:            strings.TrimSpace(*from),
		Only:            onlyStages,
		SummaryJSONPath: strings.TrimSpace(*summaryJSON),
		MaxParallel:     *maxParallel,
		DryRun:          *dryRun,
		SkipPreflight:   *skipPreflight,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR | %v\n", err)
		os.Exit(1)
	}
}

func parseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}
