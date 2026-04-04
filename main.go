package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/fatih/color"
	"gopkg.in/yaml.v3"
)

// PipelineConfig defines the structure of our pipeline.yaml
type PipelineConfig struct {
	Project string  `yaml:"project"`
	Stages  []Stage `yaml:"stages"`
}

type Stage struct {
	Name     string   `yaml:"name"`
	Command  string   `yaml:"command"`
	Args     []string `yaml:"args"`
	Parallel bool     `yaml:"parallel"`
}

var (
	info    = color.New(color.FgCyan).Add(color.Bold)
	success = color.New(color.FgGreen).Add(color.Bold)
	failure = color.New(color.FgRed).Add(color.Bold)
)

func main() {
	// 1. Load the pipeline configuration
	yamlFile, err := os.ReadFile("pipeline.yaml")
	if err != nil {
		failure.Printf("❌ Error: Could not find pipeline.yaml: %v\n", err)
		os.Exit(1)
	}

	var config PipelineConfig
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		failure.Printf("❌ Error parsing YAML: %v\n", err)
		os.Exit(1)
	}

	info.Printf("🚀 Starting Pipeline: %s\n", config.Project)
	fmt.Println(strings.Repeat("=", 40))

	var wg sync.WaitGroup

	// 2. Iterate through stages
	for _, stage := range config.Stages {
		if stage.Parallel {
			wg.Add(1)
			// Launch parallel stage in a Goroutine
			go func(s Stage) {
				defer wg.Done()
				if err := executeStage(s); err != nil {
					failure.Printf("❌ Parallel Stage '%s' failed. Terminating.\n", s.Name)
					os.Exit(1)
				}
			}(stage)
		} else {
			// Wait for any running parallel stages to finish before a sequential stage
			wg.Wait()
			if err := executeStage(stage); err != nil {
				failure.Printf("❌ Sequential Stage '%s' failed. Terminating.\n", stage.Name)
				os.Exit(1)
			}
		}
	}

	// 3. Final wait for any remaining parallel tasks
	wg.Wait()
	fmt.Println(strings.Repeat("=", 40))
	success.Println("🎉 PIPELINE SUCCESS: All stages passed!")
}

// executeStage handles the actual system calls
func executeStage(s Stage) error {
	info.Printf("\n▶️  RUNNING: %s\n", s.Name)
	fmt.Printf("   Command: %s %s\n", s.Command, strings.Join(s.Args, " "))

	cmd := exec.Command(s.Command, s.Args...)

	// Stream output to the main terminal
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return err
	}

	success.Printf("✅ COMPLETED: %s\n", s.Name)
	return nil
}
