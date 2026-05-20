package main

import (
	"os"

	"github.com/thieso2/sandcastle-incus/internal/cli"
)

func main() {
	os.Exit(cli.Execute("sc", os.Args[1:]))
}
