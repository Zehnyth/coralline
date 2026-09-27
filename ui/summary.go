package ui

import (
	"sort"
	"time"

	"coralline/app/domain"
)

// moduleSummary is one module as the listings show it.
type moduleSummary struct {
	domain.Module
	// Latest is the default version: the newest stable release, or the
	// newest version when every release is a pre-release.
	Latest         string
	LatestIsStable bool
	VersionCount   int
	// UpdatedAt is when the module's most recent version was published.
	UpdatedAt time.Time
}

type moduleKey struct{ namespace, name, system string }

// summarize folds published versions into one summary per module, sorted
// by namespace, name, then system.
func summarize(versions []domain.Version) []moduleSummary {
	byModule := map[moduleKey][]domain.Version{}
	var keys []moduleKey
	for _, v := range versions {
		k := moduleKey{v.Namespace, v.Name, v.System}
		if _, seen := byModule[k]; !seen {
			keys = append(keys, k)
		}
		byModule[k] = append(byModule[k], v)
	}
	out := make([]moduleSummary, 0, len(keys))
	for _, k := range keys {
		vs := byModule[k]
		names := make([]string, 0, len(vs))
		s := moduleSummary{Module: domain.Module{Namespace: k.namespace, Name: k.name, System: k.system}, VersionCount: len(vs)}
		for _, v := range vs {
			names = append(names, v.Version)
			if v.PublishedAt.After(s.UpdatedAt) {
				s.UpdatedAt = v.PublishedAt
			}
		}
		sortVersionsDesc(names)
		s.Latest, s.LatestIsStable = latestStable(names)
		out = append(out, s)
	}
	sortSummaries(out)
	return out
}

func sortSummaries(mods []moduleSummary) {
	sort.SliceStable(mods, func(i, j int) bool {
		if mods[i].Namespace != mods[j].Namespace {
			return mods[i].Namespace < mods[j].Namespace
		}
		if mods[i].Name != mods[j].Name {
			return mods[i].Name < mods[j].Name
		}
		return mods[i].System < mods[j].System
	})
}

// recentlyUpdated returns up to n modules, most recently published first.
func recentlyUpdated(mods []moduleSummary, n int) []moduleSummary {
	out := append([]moduleSummary(nil), mods...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}
