// Package semver computes the next release tag from the current one and a
// bump kind. The release workflow reads the bump kind from the merged pull
// request's major, minor, or patch label, so the arithmetic lives here where
// it can be unit tested rather than buried in workflow YAML or shell.
package semver

import (
	"fmt"
	"regexp"
	"strconv"
)

var tagPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

// Bump names the size of a release. These are exactly the labels the release
// workflow requires on a pull request.
type Bump string

const (
	Major Bump = "major"
	Minor Bump = "minor"
	Patch Bump = "patch"
)

// Next computes the tag that follows current for the given bump. An empty
// current is treated as v0.0.0, so the first release of a repository with no
// tags lands on v1.0.0, v0.1.0, or v0.0.1.
func Next(current string, bump Bump) (string, error) {
	if current == "" {
		current = "v0.0.0"
	}

	match := tagPattern.FindStringSubmatch(current)
	if match == nil {
		return "", fmt.Errorf("current tag is not a release version: %s", current)
	}

	// ParseUint with base 10 rejects a component such as 08, which Go (like
	// the shell) would otherwise reject as an invalid octal literal. A tag
	// that carries one should not take the release pipeline down with a
	// parse panic; it fails cleanly here instead.
	major, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		return "", fmt.Errorf("current tag is not a release version: %s", current)
	}
	minor, err := strconv.ParseUint(match[2], 10, 64)
	if err != nil {
		return "", fmt.Errorf("current tag is not a release version: %s", current)
	}
	patch, err := strconv.ParseUint(match[3], 10, 64)
	if err != nil {
		return "", fmt.Errorf("current tag is not a release version: %s", current)
	}

	switch bump {
	case Major:
		major++
		minor = 0
		patch = 0
	case Minor:
		minor++
		patch = 0
	case Patch:
		patch++
	default:
		return "", fmt.Errorf("bump must be major, minor, or patch: %s", bump)
	}

	return fmt.Sprintf("v%d.%d.%d", major, minor, patch), nil
}
