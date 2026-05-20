package main

import (
	"os"

	"github.com/thieso2/sandcastle-incus/internal/cli"
)

func main() {
	os.Exit(cli.Execute("sandcastle", os.Args[1:]))
}
