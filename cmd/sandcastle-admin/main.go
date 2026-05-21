package main

import (
	"os"
	"path/filepath"

	"github.com/thieso2/sandcastle-incus/internal/cli"
)

func main() {
	os.Exit(cli.ExecuteAdmin(commandName(os.Args[0]), os.Args[1:]))
}

func commandName(argv0 string) string {
	name := filepath.Base(argv0)
	if name == "" || name == "." {
		return "sandcastle-admin"
	}
	return name
}
