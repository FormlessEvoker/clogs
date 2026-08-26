// Command nextversion prints the release tag that follows a current tag for
// a given bump kind. It exists so the release workflow can shell out to a
// unit-tested Go binary instead of re-implementing the arithmetic in YAML or
// bash.
//
// Usage: go run ./cmd/nextversion <current-tag> <major|minor|patch>
//
// An empty current tag is treated as v0.0.0, so the first release of a
// repository with no tags lands on v1.0.0, v0.1.0, or v0.0.1.
package main

import (
	"fmt"
	"os"

	"github.com/FormlessEvoker/clogs/internal/semver"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <current-tag> <major|minor|patch>\n", os.Args[0])
		os.Exit(2)
	}

	next, err := semver.Next(os.Args[1], semver.Bump(os.Args[2]))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Println(next)
}
