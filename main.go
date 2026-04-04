package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ivanx000/Pipeline/internal/pipeline"
)

func main() {
	configPath := flag.String("config", "pipeline.yaml", "Path to pipeline configuration file")
	flag.Parse()

	if err := pipeline.Run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}
