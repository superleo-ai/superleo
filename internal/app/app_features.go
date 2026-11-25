package app

import "fmt"

// Features is whether each switchable part is on: browser, desktop, coding,
// wellbeing, finance (backend/features.go). The window, the web page and the
// phones hide what is off.
func (a *App) Features() map[string]bool {
	s := a.GetSettings()
	return s.Features()
}

// SetFeature switches one part on or off. Saving rebuilds the service, so
// the part's tools come or go with it.
func (a *App) SetFeature(name string, on bool) (map[string]bool, error) {
	s := a.GetSettings()
	if !s.SetFeature(name, on) {
		return nil, &callError{400, fmt.Sprintf("no feature called %q", name)}
	}
	if err := a.SaveSettings(s); err != nil {
		return nil, err
	}
	return a.Features(), nil
}
