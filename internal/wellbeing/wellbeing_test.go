package wellbeing

import (
	"context"
	"testing"
	"time"
)

func testModule(t *testing.T) *Module {
	t.Helper()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	m, err := Open(t.TempDir(), loc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	m.now = func() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, loc) }
	return m
}

func TestAReportMergesIntoTheDay(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	// The full report of the 19th, then a later one that only has its series.
	full := map[string]any{"date": "2026-10-19", "resting_hr": 58.0, "recovery": 72.0, "sleep": map[string]any{"asleep_h": 7.2, "deep_h": 1.1}}
	if _, err := m.Report(ctx, []map[string]any{full}); err != nil {
		t.Fatal(err)
	}
	later := map[string]any{"date": "2026-10-19", "resting_hr": 57.0, "steps": 9100.0}
	if _, err := m.Report(ctx, []map[string]any{later}); err != nil {
		t.Fatal(err)
	}
	out, err := m.Days(ctx, DaysIn{From: "2026-10-19", To: "2026-10-19"})
	if err != nil || len(out.Days) != 1 {
		t.Fatalf("days %v %v", out, err)
	}
	d := out.Days[0]
	if d["resting_hr"] != 57.0 || d["steps"] != 9100.0 || d["recovery"] != 72.0 || lookup(d, "sleep.deep_h") != 1.1 {
		t.Fatalf("merged day %v", d)
	}
	if _, err := m.Report(ctx, []map[string]any{{"date": "yesterday"}}); err == nil {
		t.Fatal("a bad date was taken")
	}
}

func TestTheToolsReadTodayARangeAndATrend(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	var days []map[string]any
	deep := []float64{1.0, 1.1, 1.2}
	for i, hr := range []float64{60, 58, 57} {
		days = append(days, map[string]any{"date": time.Date(2026, 10, 18+i, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), "resting_hr": hr, "sleep": map[string]any{"deep_h": deep[i]}})
	}
	if _, err := m.Report(ctx, days); err != nil {
		t.Fatal(err)
	}
	today, err := m.Today(ctx)
	if err != nil || !today.IsToday || today.Day["resting_hr"] != 57.0 {
		t.Fatalf("today %+v %v", today, err)
	}
	rng, err := m.Days(ctx, DaysIn{Days: 2})
	if err != nil || rng.From != "2026-10-19" || len(rng.Days) != 2 {
		t.Fatalf("days %+v %v", rng, err)
	}
	tr, err := m.Trend(ctx, TrendIn{Metric: "sleep.deep_h", Days: 7})
	if err != nil || tr.Count != 3 || tr.Min != 1.0 || tr.Latest != 1.2 {
		t.Fatalf("trend %+v %v", tr, err)
	}
	st, err := m.Status(ctx)
	if err != nil || st.Days != 3 || st.Last != "2026-10-20" {
		t.Fatalf("status %+v %v", st, err)
	}
}

func TestTheKnowledgeBaseIsWellFormed(t *testing.T) {
	if len(kb.Entries) < 110 {
		t.Fatalf("only %d entries", len(kb.Entries))
	}
	for _, r := range kb.Ranges {
		if _, ok := kb.byID[r.Knowledge]; !ok {
			t.Errorf("%s links to missing entry %s", r.Metric, r.Knowledge)
		}
		if n := len(r.Bands); n == 0 || r.Bands[n-1].Below != nil {
			t.Errorf("%s: the last band must be open", r.Metric)
		}
	}
}

func TestKnowledgeSearchFindsChineseAndPutsUrgentFirst(t *testing.T) {
	hits := kb.search("我最近血压有点高怎么办", 3)
	if len(hits) == 0 || hits[0].ID != "bp-categories" {
		t.Fatalf("blood pressure: %+v", hits)
	}
	hits = kb.search("跑步的时候胸痛，还有点胸闷", 3)
	if len(hits) == 0 || hits[0].ID != "urgent-chest-pain" {
		t.Fatalf("chest pain: %+v", hits)
	}
	hits = kb.search("Why was my HRV lower after drinking alcohol?", 3)
	found := false
	for _, h := range hits {
		found = found || h.ID == "hrv-factors" || h.ID == "alcohol-sleep"
	}
	if !found {
		t.Fatalf("hrv alcohol: %+v", hits)
	}
}

func TestAssessmentsFollowTheGuidelines(t *testing.T) {
	cases := []struct {
		metric string
		value  float64
		age    int
		sex    string
		level  int
		label  string
	}{
		{"resting_hr", 52, 0, "", 0, "low (fit)"},
		{"resting_hr", 105, 0, "", 2, "high (tachycardia)"},
		{"spo2", 89, 0, "", 3, "low"},
		{"glucose_fasting", 6.4, 0, "", 2, "impaired fasting glucose"},
		{"glucose_fasting", 7.2, 0, "", 3, "diabetes range"},
		{"bmi_china", 25, 0, "", 1, "overweight (China)"},
		{"bmi", 25, 0, "", 1, "overweight (WHO)"},
		{"sleep_hours", 7.5, 70, "", 0, "recommended"},
		{"sleep_hours", 8.6, 70, "", 1, "long"},
		{"vo2max", 44, 35, "male", 0, "good"},
		{"vo2max", 34, 35, "male", 1, "poor"},
		{"vo2max", 44, 35, "female", 0, "superior"},
		{"hrv_change_pct", -30, 0, "", 2, "well below baseline"},
		{"uric_acid", 450, 0, "male", 2, "high (hyperuricaemia)"},
	}
	for _, c := range cases {
		a, err := kb.assess(c.metric, c.value, c.age, c.sex)
		if err != nil || a.Level != c.level || a.Label != c.label {
			t.Errorf("%s %v (age %d %s): got %+v %v, want level %d %q", c.metric, c.value, c.age, c.sex, a, err, c.level, c.label)
		}
	}
	if _, err := kb.assess("vo2max", 40, 0, ""); err == nil {
		t.Error("vo2max without age and sex should say what it needs")
	}
	bp, err := kb.assessBloodPressure(128, 92)
	if err != nil || bp.Level != 3 || bp.Label != "stage 2 hypertension" {
		t.Fatalf("bp 128/92: %+v %v", bp, err)
	}
}
