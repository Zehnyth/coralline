package sqlitestore

import (
	"strings"

	sqlite "modernc.org/sqlite"
)

// isUniqueViolation detects a UNIQUE constraint failure specifically —
// this is what gives the two-phase publish protocol's reservation step
// its atomic conflict detection. We check the driver's typed error code
// first and fall back to a string match so a driver version bump that
// changes wrapping doesn't silently break detection.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if serr, ok := err.(*sqlite.Error); ok {
		const (
			sqliteConstraint       = 19
			sqliteConstraintUnique = 2067
			sqliteConstraintPK     = 1555
		)
		code := serr.Code()
		if code == sqliteConstraintUnique || code == sqliteConstraintPK || code == sqliteConstraint {
			return true
		}
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
