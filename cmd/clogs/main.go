package main

import (
	"io"
	"os"

	"github.com/FormlessEvoker/clogs/internal/cli"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return cli.Run(args, stdout, stderr)
}
