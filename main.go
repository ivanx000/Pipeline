package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/fatih/color" // Import the new library
)

func main() {
	// Create reusable color "formatters"
	info := color.New(color.FgCyan).Add(color.Bold)
	success := color.New(color.FgGreen).Add(color.Bold)
	failure := color.New(color.FgRed).Add(color.Bold)

	if len(os.Args) < 2 {
		failure.Println("❌ Usage: go run main.go <command>")
		os.Exit(1)
	}

	commandName := os.Args[1]
	args := os.Args[2:]

	// Use the Info color for the header
	info.Printf("🚀 STAGE START: %s %s\n", commandName, strings.Join(args, " "))
	fmt.Println(strings.Repeat("-", 40))

	cmd := exec.Command(commandName, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()

	fmt.Println(strings.Repeat("-", 40))

	if err != nil {
		failure.Printf("❌ STAGE FAILED: %v\n", err)
		os.Exit(1)
	}

	// Use the Success color for the footer
	success.Println("✅ STAGE COMPLETED SUCCESSFULLY!")
}
