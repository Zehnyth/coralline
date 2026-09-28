package ui

import (
	"sort"
	"strings"
)

// sortVersionsDesc orders versions newest first by semver precedence, so
// 1.10.0 follows 1.9.0.
func sortVersionsDesc(versions []string) {
	sort.SliceStable(versions, func(i, j int) bool { return compareVersions(versions[i], versions[j]) > 0 })
}

// latestStable returns the newest release without a pre-release suffix from
// versions sorted newest first. When every version is a pre-release it
// returns the newest one and stable=false.
func latestStable(sorted []string) (version string, stable bool) {
	if len(sorted) == 0 {
		return "", false
	}
	for _, candidate := range sorted {
		if !isPrerelease(candidate) {
			return candidate, true
		}
	}
	return sorted[0], false
}

func isPrerelease(version string) bool {
	core, _, _ := strings.Cut(version, "+")
	return strings.Contains(core, "-")
}

// compareVersions compares semver components without converting potentially
// large integers. Build metadata does not affect precedence.
func compareVersions(a, b string) int {
	a = strings.TrimPrefix(strings.SplitN(a, "+", 2)[0], "v")
	b = strings.TrimPrefix(strings.SplitN(b, "+", 2)[0], "v")
	ac, ap, _ := strings.Cut(a, "-")
	bc, bp, _ := strings.Cut(b, "-")
	compareNumeric := func(x, y string) int {
		x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
		if len(x) < len(y) {
			return -1
		}
		if len(x) > len(y) {
			return 1
		}
		return strings.Compare(x, y)
	}
	ax, bx := strings.Split(ac, "."), strings.Split(bc, ".")
	for i := 0; i < len(ax) && i < len(bx); i++ {
		if c := compareNumeric(ax[i], bx[i]); c != 0 {
			return c
		}
	}
	if ap == bp {
		return 0
	}
	// Per semver precedence rules, a version with a pre-release suffix
	// always sorts before the same version without one.
	if ap == "" {
		return 1
	}
	if bp == "" {
		return -1
	}
	isNumeric := func(s string) bool {
		if s == "" {
			return false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	// Compare pre-release identifiers dot-separated field by field: numeric
	// fields compare numerically, alphanumeric fields compare lexically, and
	// a numeric field always sorts below an alphanumeric one at the same
	// position.
	ax, bx = strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(ax) && i < len(bx); i++ {
		xn, yn := isNumeric(ax[i]), isNumeric(bx[i])
		if xn && yn {
			if c := compareNumeric(ax[i], bx[i]); c != 0 {
				return c
			}
		} else if xn {
			return -1
		} else if yn {
			return 1
		} else if c := strings.Compare(ax[i], bx[i]); c != 0 {
			return c
		}
	}
	// A shorter set of pre-release fields sorts lower when every shared
	// field compared equal.
	if len(ax) < len(bx) {
		return -1
	}
	if len(ax) > len(bx) {
		return 1
	}
	return 0
}
