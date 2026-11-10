package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superleo/internal/backend"
	"github.com/liliang-cn/superleo/internal/wellbeing"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Wellbeing: the person's health, one summary per day, as the iPhone reads it
// from Apple Health (internal/wellbeing). The phone reports it — SuperLeo's
// own app with WellbeingReport, Vita through /api/vita, the route its sync
// already speaks — and the desktop's Wellbeing page, SuperLeo's model and the
// queen's engines read it.

var wellbeingOnce struct {
	sync.Once
	m   *wellbeing.Module
	err error
}

// wellbeingModule opens the store in SuperLeo's data directory the first time
// it is asked for.
func (a *App) wellbeingModule() (*wellbeing.Module, error) {
	wellbeingOnce.Do(func() {
		wellbeingOnce.m, wellbeingOnce.err = wellbeing.Open(backend.DataDir(), wellbeingZone())
		if wellbeingOnce.err != nil {
			slog.Warn("wellbeing: could not open its store", "error", wellbeingOnce.err)
		}
	})
	return wellbeingOnce.m, wellbeingOnce.err
}

// wellbeingZone is the person's zone, for what "today" is: SUPERLEO_TZ when
// set, else the machine's, else — on a server in UTC, as the hive's pods
// are — Asia/Shanghai, where the person lives.
func wellbeingZone() *time.Location {
	if z := strings.TrimSpace(os.Getenv("SUPERLEO_TZ")); z != "" {
		if loc, err := time.LoadLocation(z); err == nil {
			return loc
		}
	}
	if time.Local.String() != "UTC" && time.Local.String() != "" {
		return time.Local
	}
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.Local
}

// WellbeingStatus is how many days are kept and when the phone last reported.
func (a *App) WellbeingStatus() (wellbeing.Status, error) {
	m, err := a.wellbeingModule()
	if err != nil {
		return wellbeing.Status{}, err
	}
	return m.Status(context.Background())
}

// WellbeingToday is today's summary, or the newest day.
func (a *App) WellbeingToday() (*wellbeing.TodayOut, error) {
	m, err := a.wellbeingModule()
	if err != nil {
		return nil, err
	}
	return m.Today(context.Background())
}

// WellbeingDays is the last days, oldest first.
func (a *App) WellbeingDays(days int) (*wellbeing.DaysOut, error) {
	m, err := a.wellbeingModule()
	if err != nil {
		return nil, err
	}
	return m.Days(context.Background(), wellbeing.DaysIn{Days: days})
}

// WellbeingTrend is one metric over the last days.
func (a *App) WellbeingTrend(metric string, days int) (*wellbeing.TrendOut, error) {
	m, err := a.wellbeingModule()
	if err != nil {
		return nil, err
	}
	return m.Trend(context.Background(), wellbeing.TrendIn{Metric: metric, Days: days})
}

// WellbeingReport is the phone's report: days merged by date.
func (a *App) WellbeingReport(days []map[string]any) (int, error) {
	m, err := a.wellbeingModule()
	if err != nil {
		return 0, err
	}
	n, err := m.Report(context.Background(), days)
	if err == nil {
		slog.Info("wellbeing: report stored", "days", n)
		a.emit("wellbeing:changed", map[string]any{"days": n})
	}
	return n, err
}

// registerWellbeingTools gives SuperLeo's model the health tools.
func (a *App) registerWellbeingTools(svc *backend.Service) {
	if svc == nil {
		return
	}
	inner := svc.Agent()
	if inner == nil {
		return
	}
	ro := agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true}
	asJSON := func(v any, err error) (any, error) {
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(v)
		return string(b), err
	}
	// args arrive as a map; the module's input structs are filled from it.
	fill := func(args map[string]any, into any) error {
		b, err := json.Marshal(args)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, into)
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	num := func(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}

	inner.AddToolWithMetadata("health_today", wellbeing.DescToday, obj(map[string]any{}),
		func(ctx context.Context, _ map[string]any) (any, error) {
			m, err := a.wellbeingModule()
			if err != nil {
				return nil, err
			}
			return asJSON(m.Today(ctx))
		}, ro)
	inner.AddToolWithMetadata("health_days", wellbeing.DescDays, obj(map[string]any{
		"from": str("first day, YYYY-MM-DD; default: days before to"),
		"to":   str("last day, YYYY-MM-DD, inclusive; default today"),
		"days": num("how many days back from to when from is not given; default 7, at most 366"),
	}), func(ctx context.Context, args map[string]any) (any, error) {
		var in wellbeing.DaysIn
		if err := fill(args, &in); err != nil {
			return nil, err
		}
		m, err := a.wellbeingModule()
		if err != nil {
			return nil, err
		}
		return asJSON(m.Days(ctx, in))
	}, ro)
	inner.AddToolWithMetadata("health_trend", wellbeing.DescTrend, obj(map[string]any{
		"metric": str("a numeric key of a day: resting_hr, hrv_ms, sleep_h, steps, spo2_pct, weight_kg, recovery, stress, active_kcal, exercise_min, respiratory_rate, wrist_temp_c, glucose_mmol, systolic, diastolic, vo2max; nested with a dot, e.g. sleep.deep_h"),
		"days":   num("how many days back from today; default 30, at most 366"),
	}, "metric"), func(ctx context.Context, args map[string]any) (any, error) {
		var in wellbeing.TrendIn
		if err := fill(args, &in); err != nil {
			return nil, err
		}
		m, err := a.wellbeingModule()
		if err != nil {
			return nil, err
		}
		return asJSON(m.Trend(ctx, in))
	}, ro)
	inner.AddToolWithMetadata("health_knowledge", wellbeing.DescKnowledge, obj(map[string]any{
		"query": str("the question or topic, any language"),
		"limit": num("how many entries, default 3, at most 10"),
	}, "query"), func(_ context.Context, args map[string]any) (any, error) {
		var in wellbeing.KnowledgeIn
		if err := fill(args, &in); err != nil {
			return nil, err
		}
		return asJSON(wellbeing.Knowledge(in))
	}, ro)
	inner.AddToolWithMetadata("health_assess", wellbeing.DescAssess, obj(map[string]any{
		"metric": str("blood_pressure (value systolic, value2 diastolic), resting_hr, spo2, sleep_hours, steps, bmi, bmi_china, body_fat, glucose_fasting, hba1c, ldl, hdl, triglycerides, uric_acid, hrv_change_pct, rhr_change, vo2max, …"),
		"value":  num("the number, in the metric's unit"),
		"value2": num("diastolic, for blood_pressure"),
		"age":    num("age in years; needed for vo2max"),
		"sex":    str("male or female; needed for vo2max, body_fat, waist, hdl, uric_acid"),
	}, "metric", "value"), func(_ context.Context, args map[string]any) (any, error) {
		var in wellbeing.AssessIn
		if err := fill(args, &in); err != nil {
			return nil, err
		}
		return asJSON(wellbeing.Assess(in))
	}, ro)
}

// wellbeingMCPTools are the health tools on SuperLeo's MCP, for the queen's
// engines (Codex, Claude Code) and any MCP client.
var wellbeingMCPTools = []string{
	"superleo_health_today", "superleo_health_days", "superleo_health_trend",
	"superleo_health_knowledge", "superleo_health_assess",
}

type healthTodayIn struct{}

func addWellbeingMCPTools(s *mcp.Server, app *App) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mod := func() (*wellbeing.Module, error) { return app.wellbeingModule() }
	mcp.AddTool(s, &mcp.Tool{Name: "superleo_health_today", Description: wellbeing.DescToday, Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ healthTodayIn) (*mcp.CallToolResult, *wellbeing.TodayOut, error) {
			m, err := mod()
			if err != nil {
				return nil, nil, err
			}
			out, err := m.Today(ctx)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "superleo_health_days", Description: wellbeing.DescDays, Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in wellbeing.DaysIn) (*mcp.CallToolResult, *wellbeing.DaysOut, error) {
			m, err := mod()
			if err != nil {
				return nil, nil, err
			}
			out, err := m.Days(ctx, in)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "superleo_health_trend", Description: wellbeing.DescTrend, Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in wellbeing.TrendIn) (*mcp.CallToolResult, *wellbeing.TrendOut, error) {
			m, err := mod()
			if err != nil {
				return nil, nil, err
			}
			out, err := m.Trend(ctx, in)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "superleo_health_knowledge", Description: wellbeing.DescKnowledge, Annotations: ro},
		func(_ context.Context, _ *mcp.CallToolRequest, in wellbeing.KnowledgeIn) (*mcp.CallToolResult, *wellbeing.KnowledgeOut, error) {
			out, err := wellbeing.Knowledge(in)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "superleo_health_assess", Description: wellbeing.DescAssess, Annotations: ro},
		func(_ context.Context, _ *mcp.CallToolRequest, in wellbeing.AssessIn) (*mcp.CallToolResult, *wellbeing.AssessOut, error) {
			out, err := wellbeing.Assess(in)
			return nil, out, err
		})
}

// vitaRoutes is what the Vita app's sync speaks, under /api/vita: its server
// address set to https://<SuperLeo>/api/vita and a paired device's token,
// it reports here as it did to its own server.
//
//	POST /api/vita/api/days    {"days":[{...},...]}  merged by date
//	GET  /api/vita/api/status  how many days, the span, the last report
//
// Under /api/, so the gate (auth.go) already wants a token or a session.
func vitaRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("POST /api/vita/api/days", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Days []map[string]any `json:"days"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "want {\"days\":[...]}: " + err.Error()})
			return
		}
		n, err := app.WellbeingReport(body.Days)
		if err != nil {
			code := http.StatusBadRequest
			var pe *os.PathError
			if errors.As(err, &pe) {
				code = http.StatusInternalServerError
			}
			writeJSONStatus(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]int{"stored": n})
	})
	mux.HandleFunc("GET /api/vita/api/status", func(w http.ResponseWriter, r *http.Request) {
		st, err := app.WellbeingStatus()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusOK, st)
	})
}
