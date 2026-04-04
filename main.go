package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ivanx000/Pipeline/internal/pipeline"
)

func main() {
	configPath := flag.String("config", "pipeline.yaml", "Path to pipeline configuration file")
	quiet := flag.Bool("quiet", false, "Suppress informational logs and only show warnings/errors")
	flag.Parse()

	if err := pipeline.RunWithOptions(*configPath, pipeline.Options{Quiet: *quiet}); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR | %v\n", err)
		os.Exit(1)
	}
}
