package pipeline

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func loadConfig(configPath string) (Config, error) {
	yamlFile, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("error reading config %q: %w", configPath, err)
	}

	var config Config
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		return Config{}, fmt.Errorf("error parsing %q: %w", configPath, err)
	}

	if err := validateConfig(config); err != nil {
		return Config{}, err
	}

	return config, nil
}

func validateConfig(config Config) error {
	if len(config.Stages) == 0 {
		return errors.New("pipeline has no stages")
	}

	names := make(map[string]struct{}, len(config.Stages))
	for i, stage := range config.Stages {
		if strings.TrimSpace(stage.Name) == "" {
			return fmt.Errorf("stage at index %d is missing name", i)
		}
		if strings.TrimSpace(stage.Command) == "" {
			return fmt.Errorf("stage %q is missing command", stage.Name)
		}
		if stage.Retry < 0 {
			return fmt.Errorf("stage %q has invalid retry value %d; must be >= 0", stage.Name, stage.Retry)
		}
		if stage.Timeout != "" {
			timeout, err := time.ParseDuration(stage.Timeout)
			if err != nil {
				return fmt.Errorf("stage %q has invalid timeout %q: %w", stage.Name, stage.Timeout, err)
			}
			if timeout <= 0 {
				return fmt.Errorf("stage %q has non-positive timeout %q", stage.Name, stage.Timeout)
			}
		}
		if _, exists := names[stage.Name]; exists {
			return fmt.Errorf("duplicate stage name %q", stage.Name)
		}
		names[stage.Name] = struct{}{}
	}

	return nil
}
