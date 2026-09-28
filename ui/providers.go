package ui

import (
	"hash/fnv"
	"sort"
	"strconv"
)

// Modules target any Terraform provider — clouds, source control,
// observability, in-house providers — so provider display must work for
// systems nobody listed. knownProviders only improves names and colours; an
// unknown system still gets a readable name, the "Other" category, and a
// stable colour tone hashed from its name.
type providerInfo struct {
	System   string
	Name     string
	Category string
	// Tone is "0".."7" (a hue in base.css) or "n" (neutral).
	Tone string
}

// Provider categories, in the order the provider filter lists them.
const (
	catCloud         = "Cloud"
	catSourceControl = "Source control"
	catObservability = "Observability"
	catContainers    = "Containers"
	catNetworking    = "Networking"
	catSecurity      = "Security"
	catData          = "Data"
	catOther         = "Other"
)

var categoryOrder = []string{catCloud, catSourceControl, catObservability, catContainers, catNetworking, catSecurity, catData, catOther}

var knownProviders = map[string]providerInfo{
	"aws":          {Name: "AWS", Category: catCloud, Tone: "0"},
	"google":       {Name: "Google Cloud", Category: catCloud, Tone: "1"},
	"azurerm":      {Name: "Azure", Category: catCloud, Tone: "2"},
	"github":       {Name: "GitHub", Category: catSourceControl, Tone: "n"},
	"gitlab":       {Name: "GitLab", Category: catSourceControl, Tone: "6"},
	"bitbucket":    {Name: "Bitbucket", Category: catSourceControl, Tone: "1"},
	"newrelic":     {Name: "New Relic", Category: catObservability, Tone: "3"},
	"datadog":      {Name: "Datadog", Category: catObservability, Tone: "5"},
	"grafana":      {Name: "Grafana", Category: catObservability, Tone: "6"},
	"pagerduty":    {Name: "PagerDuty", Category: catObservability, Tone: "3"},
	"kubernetes":   {Name: "Kubernetes", Category: catContainers, Tone: "1"},
	"helm":         {Name: "Helm", Category: catContainers, Tone: "2"},
	"cloudflare":   {Name: "Cloudflare", Category: catNetworking, Tone: "6"},
	"vault":        {Name: "Vault", Category: catSecurity, Tone: "0"},
	"okta":         {Name: "Okta", Category: catSecurity, Tone: "1"},
	"snowflake":    {Name: "Snowflake", Category: catData, Tone: "2"},
	"mongodbatlas": {Name: "MongoDB Atlas", Category: catData, Tone: "3"},
}

// toneCount is how many hue tones base.css defines (--tone-0..7).
const toneCount = 8

func lookupProvider(system string) providerInfo {
	if p, ok := knownProviders[system]; ok {
		p.System = system
		return p
	}
	return providerInfo{System: system, Name: capitalize(system), Category: catOther, Tone: strconv.Itoa(hashTone(system))}
}

func providerName(system string) string { return lookupProvider(system).Name }

func providerTone(system string) string { return lookupProvider(system).Tone }

// hashTone maps a name to one of the hue tones, so the same name always
// gets the same colour however a list is sorted or filtered.
func hashTone(name string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return int(h.Sum32() % toneCount)
}

// providerOption is one entry in the provider filter.
type providerOption struct {
	System string
	Name   string
	Count  int
}

// providerGroup is one <optgroup> of the provider filter.
type providerGroup struct {
	Category string
	Options  []providerOption
}

// groupProviders counts modules per provider and groups the result by
// category (in categoryOrder), most-used provider first within a group.
func groupProviders(mods []moduleSummary) []providerGroup {
	counts := map[string]int{}
	for _, m := range mods {
		counts[m.System]++
	}
	byCategory := map[string][]providerOption{}
	for system, n := range counts {
		p := lookupProvider(system)
		byCategory[p.Category] = append(byCategory[p.Category], providerOption{System: system, Name: p.Name, Count: n})
	}
	var out []providerGroup
	for _, cat := range categoryOrder {
		opts := byCategory[cat]
		if len(opts) == 0 {
			continue
		}
		sort.Slice(opts, func(i, j int) bool {
			if opts[i].Count != opts[j].Count {
				return opts[i].Count > opts[j].Count
			}
			return opts[i].Name < opts[j].Name
		})
		out = append(out, providerGroup{Category: cat, Options: opts})
	}
	return out
}
