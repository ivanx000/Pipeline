package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/fatih/color"
	"gopkg.in/yaml.v3" // New import
)

// Define the structure of our pipeline file
type PipelineConfig struct {
	Project string `yaml:"project"`
	Stages  []struct {
		Name    string   `yaml:"name"`
		Command string   `yaml:"command"`
		Args    []string `yaml:"args"`
	} `yaml:"stages"`
}

func main() {
	info := color.New(color.FgCyan).Add(color.Bold)
	success := color.New(color.FgGreen).Add(color.Bold)
	failure := color.New(color.FgRed).Add(color.Bold)

	// 1. Read the YAML file
	yamlFile, err := os.ReadFile("pipeline.yaml")
	if err != nil {
		failure.Printf("❌ Error reading pipeline.yaml: %v\n", err)
		os.Exit(1)
	}

	// 2. Parse (Unmarshal) the YAML into our Go struct
	var config PipelineConfig
	err = yaml.Unmarshal(yamlFile, &config)
	if err != nil {
		failure.Printf("❌ Error parsing YAML: %v\n", err)
		os.Exit(1)
	}

	info.Printf("🚀 Starting Pipeline for Project: %s\n", config.Project)

	// 3. Loop through and execute each stage
	for _, stage := range config.Stages {
		info.Printf("\n▶️ Running Stage: %s\n", stage.Name)
		fmt.Printf("Command: %s %s\n", stage.Command, strings.Join(stage.Args, " "))
		fmt.Println(strings.Repeat("-", 30))

		cmd := exec.Command(stage.Command, stage.Args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			failure.Printf("❌ Stage '%s' failed: %v\n", stage.Name, err)
			os.Exit(1) // Stop the pipeline if a stage fails
		}
		success.Printf("✅ Stage '%s' completed.\n", stage.Name)
	}

	fmt.Println("\n" + strings.Repeat("=", 30))
	success.Println("🎉 ALL STAGES PASSED!")
}
