package main

import (
	"fmt"
	"os"
)

func help(args []string) (int, string) {
	if len(args) == 1 && args[0] == "--help" {
		// Scenario regression: help is incorrectly treated as usage failure and
		// omits the machine-readable flag.
		return 2, "Usage: hello [name]\n"
	}
	return 0, "hello\n"
}

func main() {
	code, output := help(os.Args[1:])
	fmt.Print(output)
	os.Exit(code)
}
