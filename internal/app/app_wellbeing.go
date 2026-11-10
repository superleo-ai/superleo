package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// Wellbeing: the person's health, one summary per day as the iPhone reads it
// from Apple Health, kept by the vita server (apps/health/server) and reached
// the way noted is, as the MCP server "vita" in mcpServers.json. The desktop's
// Wellbeing page reads it through these calls; the queen's model and engines
// use its tools (health_today, health_days, …) directly. The phone reports
// to SuperLeo (WellbeingReport), which passes the days on to vita.

// vitaRead calls one of vita's read-only tools.
func (a *App) vitaRead(tool string, args map[string]any) (any, error) {
	res := a.mcpActions().Call(context.Background(), backend.VitaServer, tool, args, backend.MCPCallOptions{
		Unlisted: true,
		Quiet:    true,
	})
	switch {
	case res.Code == backend.ActionNotConfigured:
		return nil, &callError{http.StatusServiceUnavailable, res.Error}
	case !res.OK:
		return nil, &callError{http.StatusBadGateway, res.Error}
	case res.Structured != nil:
		return res.Structured, nil
	}
	return map[string]any{"text": res.Text}, nil
}

// WellbeingToday is today's summary, or the newest day (health_today).
func (a *App) WellbeingToday() (any, error) { return a.vitaRead("health_today", map[string]any{}) }

// WellbeingDays is the last days, oldest first (health_days).
func (a *App) WellbeingDays(days int) (any, error) {
	return a.vitaRead("health_days", map[string]any{"days": days})
}

// WellbeingTrend is one metric over the last days (health_trend).
func (a *App) WellbeingTrend(metric string, days int) (any, error) {
	return a.vitaRead("health_trend", map[string]any{"metric": metric, "days": days})
}

// WellbeingStatus is whether vita is connected, how many days it keeps and
// when the phone last reported ({"connected":false} when it is not set up).
func (a *App) WellbeingStatus() (map[string]any, error) {
	if _, _, ok := backend.VitaEndpoint(); !ok {
		return map[string]any{"connected": false}, nil
	}
	var st map[string]any
	if err := vitaHTTP(http.MethodGet, "/api/status", nil, &st); err != nil {
		return nil, err
	}
	st["connected"] = true
	return st, nil
}

// WellbeingReport is the phone's report: the days are passed on to vita,
// which merges them by date. Vita's MCP is read-only — the phone is its only
// writer — so this is its /api/days, with the token its MCP entry carries.
func (a *App) WellbeingReport(days []map[string]any) (int, error) {
	var out struct {
		Stored int `json:"stored"`
	}
	if err := vitaHTTP(http.MethodPost, "/api/days", map[string]any{"days": days}, &out); err != nil {
		slog.Warn("wellbeing: report not passed on to vita", "days", len(days), "error", err)
		return 0, err
	}
	slog.Info("wellbeing: report passed on to vita", "days", out.Stored)
	a.emit("wellbeing:changed", map[string]any{"days": out.Stored})
	return out.Stored, nil
}

var vitaClient = &http.Client{Timeout: 20 * time.Second}

func vitaHTTP(method, path string, body, into any) error {
	base, headers, ok := backend.VitaEndpoint()
	if !ok {
		return &callError{http.StatusServiceUnavailable, "vita is not connected: add it to mcpServers.json as \"vita\""}
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := vitaClient.Do(req)
	if err != nil {
		return &callError{http.StatusBadGateway, "vita: " + err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		code := http.StatusBadGateway
		if resp.StatusCode == http.StatusBadRequest {
			code = http.StatusBadRequest
		}
		return &callError{code, fmt.Sprintf("vita answered %d: %s", resp.StatusCode, bytes.TrimSpace(b))}
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(b, into)
}
