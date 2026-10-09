package exit

import (
	"fmt"
	"sort"
	"strings"
)

// countryToRegion maps a Country (what the user types) to a concrete AWS
// region (an implementation detail the user never sees). This is the
// whole "pick a country" feature — one entry per place we can exit from.
var countryToRegion = map[string]string{
	"usa":       "us-east-1",
	"ireland":   "eu-west-1",
	"germany":   "eu-central-1",
	"britain":   "eu-west-2",
	"france":    "eu-west-3",
	"japan":     "ap-northeast-1",
	"korea":     "ap-northeast-2",
	"singapore": "ap-southeast-1",
	"australia": "ap-southeast-2",
	"india":     "ap-south-1",
	"canada":    "ca-central-1",
	"brazil":    "sa-east-1",
	"thailand":  "ap-southeast-7",
}

// RegionFor resolves a Country name (case-insensitive) to an AWS region.
func RegionFor(country string) (string, error) {
	r, ok := countryToRegion[strings.ToLower(strings.TrimSpace(country))]
	if !ok {
		return "", fmt.Errorf("exit: unknown country %q (try `poof regions`)", country)
	}
	return r, nil
}

// Countries lists the selectable Country names, sorted.
func Countries() []string {
	out := make([]string, 0, len(countryToRegion))
	for c := range countryToRegion {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// AllRegions returns every region poof might have placed an Exit in.
func AllRegions() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range countryToRegion {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}
