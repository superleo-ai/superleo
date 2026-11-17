package backend

import (
	"slices"
	"sync/atomic"
)

// Features are the parts the person can switch off as a whole: off, a part
// is gone everywhere — no page, no rail entry, no tile, no phone entry, and
// none of its tools reach the model. The browser and desktop control keep
// their own settings (BrowserMode, DesktopControl) and are off unless
// chosen; wellbeing and finance are on unless named in FeaturesOff, so an
// install that never heard of the switch keeps them.
const (
	FeatureBrowser   = "browser"
	FeatureDesktop   = "desktop"
	FeatureWellbeing = "wellbeing"
	FeatureFinance   = "finance"
)

// FeatureNames are every switch, in the order the settings show them.
var FeatureNames = []string{FeatureBrowser, FeatureDesktop, FeatureWellbeing, FeatureFinance}

// featureServers are the MCP servers a feature brings; off, they are not
// mounted (resolveMCPConfig) and the app's own calls to them are refused.
var featureServers = map[string]string{FeatureWellbeing: VitaServer, FeatureFinance: FinanceServer}

// FeatureOn reports whether one feature is on under these settings, the
// environment's overrides included.
func (s *Settings) FeatureOn(name string) bool {
	switch name {
	case FeatureBrowser:
		return s.browserMode() != BrowserModeOff
	case FeatureDesktop:
		return s.desktopControl()
	case FeatureWellbeing, FeatureFinance:
		return !slices.Contains(s.FeaturesOff, name)
	}
	return false
}

// Features is every feature's state by name.
func (s *Settings) Features() map[string]bool {
	out := make(map[string]bool, len(FeatureNames))
	for _, n := range FeatureNames {
		out[n] = s.FeatureOn(n)
	}
	return out
}

// SetFeature switches one feature in s. The browser comes back in the mode
// it was last in, or its own browser when it never had one.
func (s *Settings) SetFeature(name string, on bool) bool {
	switch name {
	case FeatureBrowser:
		if !on {
			s.BrowserMode = BrowserModeOff
			return true
		}
		if s.BrowserMode == BrowserModeChrome || s.BrowserMode == BrowserModeOwn {
			return true
		}
		s.BrowserMode = BrowserModeOwn
		if s.BrowserLastMode == BrowserModeChrome {
			s.BrowserMode = BrowserModeChrome
		}
	case FeatureDesktop:
		s.DesktopControl = on
	case FeatureWellbeing, FeatureFinance:
		off := slices.DeleteFunc(slices.Clone(s.FeaturesOff), func(v string) bool { return v == name })
		if !on {
			off = append(off, name)
		}
		if len(off) == 0 {
			off = nil
		}
		s.FeaturesOff = off
	default:
		return false
	}
	return true
}

// serversOff is the MCP servers of the features switched off, as the last
// service built read them; the package-level functions that find a server
// (FinanceConfigured, VitaEndpoint) consult it.
var serversOff atomic.Pointer[map[string]bool]

func applyFeatures(s *Settings) {
	m := map[string]bool{}
	for f, server := range featureServers {
		if !s.FeatureOn(f) {
			m[server] = true
		}
	}
	serversOff.Store(&m)
}

// ApplyFeatures records which features' servers are off before a service is
// built; the app calls it as settings load, so calls made before the first
// service is up already see it.
func ApplyFeatures(s *Settings) { applyFeatures(s) }

// ServerSwitchedOff reports whether an MCP server belongs to a feature the
// person switched off.
func ServerSwitchedOff(name string) bool {
	m := serversOff.Load()
	return m != nil && (*m)[name]
}
