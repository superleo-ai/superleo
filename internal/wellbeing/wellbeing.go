// Package wellbeing is the person's health in SuperLeo: one summary per day
// as an iPhone reads it from Apple Health (sleep, resting heart rate, HRV,
// activity, Vita's recovery and stress scores, its insights and suggestions),
// reported by SuperLeo's own iPhone app or by Vita, and kept here.
//
// It was the vita server (apps/health/server), a service of its own that
// SuperLeo reached over MCP. The store, the tools and the knowledge base are
// that server's, moved in: SuperLeo's model gets the tools natively, the
// queen's engines through SuperLeo's MCP, and the Vita app keeps reporting the
// same way, to /api/vita on SuperLeo instead of to its own server.
package wellbeing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"
)

// Instructions is what a model is told about the tools.
const Instructions = "The person's health as their iPhone reads it from Apple Health, one summary per day: " +
	"sleep (sleep_h asleep; sleep.asleep_h, deep_h, rem_h, bedtime, wake for a fully reported night), resting_hr (bpm), hrv_ms (SDNN), " +
	"spo2_pct, respiratory_rate, wrist_temp_c, steps, active_kcal, exercise_min, vo2max, weight_kg, body_fat_pct, systolic/diastolic, glucose_mmol, " +
	"Vita's scores (recovery and stress 0–100, heart_age, with hrv_baseline_ms and resting_hr_baseline), nutrition, workouts, " +
	"and the insights and suggestions written that day. A day only has what the phone knew; a missing key is not a zero. " +
	"health_assess judges a number against guideline reference ranges; health_knowledge returns short evidence-based guidance, urgent warnings first. " +
	"Dates are calendar days in the person's zone. Only the phone writes these. This is not medical advice."

// Module is the wellbeing store and what is asked of it.
type Module struct {
	st  *store
	loc *time.Location
	now func() time.Time
}

// Open opens (or creates) wellbeing.db in dir. loc decides what "today" is.
func Open(dir string, loc *time.Location) (*Module, error) {
	if loc == nil {
		loc = time.Local
	}
	st, err := openStore(filepath.Join(dir, "wellbeing.db"))
	if err != nil {
		return nil, err
	}
	return &Module{st: st, loc: loc, now: time.Now}, nil
}

func (m *Module) Close() error { return m.st.Close() }

func (m *Module) today() string { return m.now().In(m.loc).Format(time.DateOnly) }

// Report merges the phone's days into what is kept, by date.
func (m *Module) Report(ctx context.Context, days []map[string]any) (int, error) {
	return m.st.put(ctx, days, m.now())
}

// Status is how many days are kept, their span, and when the phone last sent.
type Status struct {
	Days       int    `json:"days"`
	First      string `json:"first,omitempty"`
	Last       string `json:"last,omitempty"`
	LastReport string `json:"last_report,omitempty"`
}

func (m *Module) Status(ctx context.Context) (Status, error) {
	n, f, l, u, err := m.st.status(ctx)
	return Status{Days: n, First: f, Last: l, LastReport: u}, err
}

type TodayOut struct {
	// Day is today's summary, or the newest one when today has not come yet.
	Day map[string]any `json:"day,omitempty"`
	// IsToday says whether Day is today's.
	IsToday bool `json:"is_today"`
	// LastReport is when the phone last sent anything (RFC 3339, UTC).
	LastReport string `json:"last_report,omitempty"`
	Note       string `json:"note,omitempty"`
}

func (m *Module) Today(ctx context.Context) (*TodayOut, error) {
	d, err := m.st.latest(ctx)
	if err != nil {
		return nil, err
	}
	_, _, _, updated, err := m.st.status(ctx)
	if err != nil {
		return nil, err
	}
	out := &TodayOut{Day: d, LastReport: updated}
	switch {
	case d == nil:
		out.Note = "Nothing has been reported yet: turn on Wellbeing in SuperLeo on the iPhone, or point Vita's sync at this SuperLeo."
	case d["date"] == m.today():
		out.IsToday = true
	default:
		out.Note = fmt.Sprintf("Nothing for %s yet; this is the newest day, %v.", m.today(), d["date"])
	}
	return out, nil
}

type DaysIn struct {
	From string `json:"from,omitempty" jsonschema:"first day, YYYY-MM-DD; default: days before to"`
	To   string `json:"to,omitempty" jsonschema:"last day, YYYY-MM-DD, inclusive; default today"`
	Days int    `json:"days,omitempty" jsonschema:"how many days back from to when from is not given; default 7, at most 366"`
}

type DaysOut struct {
	From string           `json:"from"`
	To   string           `json:"to"`
	Days []map[string]any `json:"days"`
}

func (m *Module) Days(ctx context.Context, in DaysIn) (*DaysOut, error) {
	from, to, err := span(in.From, in.To, in.Days, 7, m.today())
	if err != nil {
		return nil, err
	}
	days, err := m.st.between(ctx, from, to)
	if err != nil {
		return nil, err
	}
	return &DaysOut{From: from, To: to, Days: days}, nil
}

type TrendIn struct {
	Metric string `json:"metric" jsonschema:"a numeric key of a day: resting_hr, hrv_ms, sleep_h, steps, spo2_pct, weight_kg, recovery, stress, active_kcal, exercise_min, respiratory_rate, wrist_temp_c, glucose_mmol, systolic, diastolic, vo2max; a nested one with a dot, e.g. sleep.deep_h or nutrition.water_ml"`
	Days   int    `json:"days,omitempty" jsonschema:"how many days back from today; default 30, at most 366"`
}

type Point struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

type TrendOut struct {
	Metric string  `json:"metric"`
	From   string  `json:"from"`
	To     string  `json:"to"`
	Points []Point `json:"points"`
	Count  int     `json:"count"`
	Avg    float64 `json:"avg,omitempty"`
	Min    float64 `json:"min,omitempty"`
	Max    float64 `json:"max,omitempty"`
	Latest float64 `json:"latest,omitempty"`
}

func (m *Module) Trend(ctx context.Context, in TrendIn) (*TrendOut, error) {
	metric := strings.TrimSpace(in.Metric)
	if metric == "" {
		return nil, errors.New("metric is required")
	}
	from, to, err := span("", "", in.Days, 30, m.today())
	if err != nil {
		return nil, err
	}
	days, err := m.st.between(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := &TrendOut{Metric: metric, From: from, To: to, Points: []Point{}}
	sum := 0.0
	for _, d := range days {
		v, ok := number(lookup(d, metric))
		if !ok {
			continue
		}
		date, _ := d["date"].(string)
		out.Points = append(out.Points, Point{Date: date, Value: v})
		if out.Count == 0 || v < out.Min {
			out.Min = v
		}
		if out.Count == 0 || v > out.Max {
			out.Max = v
		}
		sum += v
		out.Count++
		out.Latest = v
	}
	if out.Count > 0 {
		out.Avg = math.Round(sum/float64(out.Count)*100) / 100
	}
	return out, nil
}

type KnowledgeIn struct {
	Query string `json:"query" jsonschema:"the question or topic, any language, e.g. 'HRV dropped after drinking' or '血压高怎么办'"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many entries, default 3, at most 10"`
}

type KnowledgeEntry struct {
	ID     string `json:"id"`
	Topic  string `json:"topic"`
	Text   string `json:"text"`
	Source string `json:"source"`
	Urgent bool   `json:"urgent,omitempty"`
}

type KnowledgeOut struct {
	Entries []KnowledgeEntry `json:"entries"`
}

func Knowledge(in KnowledgeIn) (*KnowledgeOut, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, errors.New("query is required")
	}
	limit := in.Limit
	if limit > 10 {
		limit = 10
	}
	out := &KnowledgeOut{Entries: []KnowledgeEntry{}}
	for _, e := range kb.search(in.Query, limit) {
		out.Entries = append(out.Entries, KnowledgeEntry{ID: e.ID, Topic: e.Topic, Text: e.Text, Source: e.Source, Urgent: e.Urgent})
	}
	return out, nil
}

type AssessIn struct {
	Metric string  `json:"metric" jsonschema:"what the value is: blood_pressure (value systolic, value2 diastolic), systolic, diastolic, resting_hr, spo2, respiratory_rate, sleep_hours, steps, bmi, bmi_china, body_fat, waist, glucose_fasting, glucose_post_meal, glucose_random, hba1c, ldl, hdl, triglycerides, total_cholesterol, uric_acid, hrv_change_pct, rhr_change, wrist_temp_delta, body_temp, vo2max, caffeine_mg, weekly_activity_min"`
	Value  float64 `json:"value" jsonschema:"the number, in the unit the metric uses (mmHg, bpm, %, h, kg/m², cm, mmol/L, µmol/L, °C, mL/kg/min, mg, min/week)"`
	Value2 float64 `json:"value2,omitempty" jsonschema:"diastolic, for blood_pressure"`
	Age    int     `json:"age,omitempty" jsonschema:"age in years; needed for vo2max and changes sleep_hours"`
	Sex    string  `json:"sex,omitempty" jsonschema:"male or female; needed for vo2max, body_fat, waist, hdl, uric_acid"`
}

type AssessOut struct {
	assessment
	Guidance *KnowledgeEntry `json:"guidance,omitempty"`
}

func Assess(in AssessIn) (*AssessOut, error) {
	metric := strings.ToLower(strings.TrimSpace(in.Metric))
	sex := strings.ToLower(strings.TrimSpace(in.Sex))
	var a assessment
	var err error
	if metric == "blood_pressure" {
		if in.Value2 <= 0 {
			return nil, errors.New("blood_pressure needs value (systolic) and value2 (diastolic)")
		}
		a, err = kb.assessBloodPressure(in.Value, in.Value2)
	} else {
		a, err = kb.assess(metric, in.Value, in.Age, sex)
	}
	if err != nil {
		return nil, err
	}
	out := &AssessOut{assessment: a}
	if e, ok := kb.byID[a.Knowledge]; ok {
		out.Guidance = &KnowledgeEntry{ID: e.ID, Topic: e.Topic, Text: e.Text, Source: e.Source}
	}
	return out, nil
}

// Tool descriptions, shared by the native tools and the MCP ones.
const (
	DescToday     = "Today's health summary: last night's sleep, resting heart rate, HRV, recovery, activity so far, and the insights and suggestions written for it. Falls back to the newest day when the phone has not reported today yet."
	DescDays      = "Daily health summaries over a range of days, oldest first: every key each day has. For a week, a month, or comparing days."
	DescTrend     = "One health metric over the last days as a series, with its average, lowest, highest and latest: how resting heart rate, HRV, sleep, steps or weight has been going."
	DescKnowledge = "Evidence-based health guidance for a question in any language: sleep, heart rate, HRV, blood pressure, glucose, lipids, weight, nutrition, training, stress, eyes, medicines. Urgent warnings (chest pain, stroke signs, …) come first when the question matches them."
	DescAssess    = "Judge one health number against guideline reference ranges: returns the category, a level (0 normal, 1 watch, 2 concern, 3 see a doctor, 4 urgent), short advice and the related guidance."
)

// span resolves a range: to defaults to today, from to days before it.
func span(from, to string, days, def int, today string) (string, string, error) {
	if to == "" {
		to = today
	}
	end, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return "", "", fmt.Errorf("to: want YYYY-MM-DD: %w", err)
	}
	if from == "" {
		if days <= 0 {
			days = def
		}
		if days > 366 {
			days = 366
		}
		from = end.AddDate(0, 0, -(days - 1)).Format(time.DateOnly)
	}
	start, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return "", "", fmt.Errorf("from: want YYYY-MM-DD: %w", err)
	}
	if start.After(end) {
		return "", "", errors.New("from is after to")
	}
	if end.Sub(start) > 366*24*time.Hour {
		return "", "", errors.New("at most 366 days at a time")
	}
	return from, to, nil
}

// lookup follows a dotted key into nested objects.
func lookup(d map[string]any, key string) any {
	var cur any = d
	for _, part := range strings.Split(key, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[part]
	}
	return cur
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case int:
		return float64(n), true
	}
	return 0, false
}
