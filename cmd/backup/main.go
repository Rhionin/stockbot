package main

import (
	"os"

	"github.com/Rhionin/stockbot/internal/backup"
)

func main() {
	os.Exit(exitCode(os.Args[1:]))
}

func exitCode(args []string) int {
	return backup.CLI(args)
}
