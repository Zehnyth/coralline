package domain

import (
	"fmt"
	"regexp"
)

// Module coordinates become blob keys and URL segments, so they are checked
// against the registry protocol's shapes before any storage is touched. The
// router unescapes %2F inside a single path value, so without this a name
// like "a/b" would reshape the key and could collide with another module's.
var (
	moduleNamePattern   = regexp.MustCompile(`^[0-9A-Za-z](?:[0-9A-Za-z_-]{0,62}[0-9A-Za-z])?$`)
	moduleSystemPattern = regexp.MustCompile(`^[0-9a-z]{1,64}$`)

	// Semantic version 2.0.0, without leading zeros on numeric identifiers.
	moduleVersionPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
		`(?:-((?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*))?` +
		`(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
)

const maxModuleVersionLen = 128

// ValidateModuleCoordinates is the one definition of a legal
// namespace/name/system/version tuple.
func ValidateModuleCoordinates(namespace, name, system, version string) error {
	switch {
	case !ValidNamespaceName(namespace):
		return fmt.Errorf("%w: namespace %q is not a valid namespace name", ErrInvalid, namespace)
	case !moduleNamePattern.MatchString(name):
		return fmt.Errorf("%w: module name must be 1-64 characters of letters, digits, '-' or '_', starting and ending with a letter or digit", ErrInvalid)
	case !moduleSystemPattern.MatchString(system):
		return fmt.Errorf("%w: module system must be 1-64 lowercase letters or digits", ErrInvalid)
	case len(version) > maxModuleVersionLen || !moduleVersionPattern.MatchString(version):
		return fmt.Errorf("%w: version must be a semantic version such as 1.2.3", ErrInvalid)
	}
	return nil
}
