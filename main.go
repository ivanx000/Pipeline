package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	// 1. Check if the user provided a command to run
	if len(os.Args) < 2 {
		fmt.Println("❌ Usage: go run main.go <command>")
		os.Exit(1)
	}

	// 2. Join the arguments into a single string (e.g., "echo hello")
	commandName := os.Args[1]
	args := os.Args[2:]

	fmt.Printf("🚀 Executing: %s %s\n", commandName, strings.Join(args, " "))
	fmt.Println("-----------------------------------------")

	// 3. Prepare the system command
	cmd := exec.Command(commandName, args...)

	// 4. Connect the command's output directly to your terminal
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// 5. Run it!
	err := cmd.Run()
	if err != nil {
		fmt.Printf("\n❌ Stage Failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("-----------------------------------------")
	fmt.Println("✅ Stage Completed Successfully!")
}