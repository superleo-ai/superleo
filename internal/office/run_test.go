package office

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The agent's two tools and `superleo doc` reach the verbs through Run, with
// arguments as a model sends them: an object, paths relative to a workspace.
func TestRunCarriesOutToolCalls(t *testing.T) {
	t.Setenv("SUPERLEO_NO_OFFICE", "1")
	ws := t.TempDir()
	resolve := ResolveIn(ws)
	call := func(tool, args string) *Report {
		t.Helper()
		var a map[string]any
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			t.Fatalf("bad test arguments: %v", err)
		}
		return Run(tool, a, resolve)
	}

	// Excel: build, then check what was built.
	rep := call(ToolExcel, `{"action":"build","path":"out/budget.xlsx","spec":`+budgetSpec+`}`)
	if !rep.OK() {
		t.Fatalf("build:\n%s", rep)
	}
	if _, err := os.Stat(filepath.Join(ws, "out", "budget.xlsx")); err != nil {
		t.Fatalf("the workbook was not written inside the workspace: %v", err)
	}
	if rep := call(ToolExcel, `{"action":"check","path":"out/budget.xlsx"}`); !rep.OK() || !strings.Contains(rep.String(), "all recalculated here") {
		t.Errorf("check:\n%s", rep)
	}

	// Excel: clean, with column lists as arrays.
	if err := os.WriteFile(filepath.Join(ws, "raw.csv"), []byte(messyCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	rep = call(ToolExcel, `{"action":"clean","source":"raw.csv","path":"clean.xlsx","key":["订单号"],"group":["地区"],"sum":["金额"],"by_month":"日期"}`)
	mustContain(t, rep.String(), "删除重复行: 1", "SUMIFS(")
	if !strings.Contains(rep.Summary(), "could not be read as their column's type") {
		t.Errorf("the summary a trace line carries does not say what is wrong: %q", rep.Summary())
	}

	// Word: build to a length, edit with tracked changes, check against the original.
	writePNG(t, filepath.Join(ws, "arch.png"))
	rep = call(ToolWord, `{"action":"build","path":"plan.docx","pages":40,"spec":`+planSpec+`}`)
	mustContain(t, rep.String(), "5 heading(s)", "about 40 pages were asked for")
	if rep.OK() {
		t.Error("a four-page document passed as about forty")
	}
	rep = call(ToolWord, `{"action":"edit","source":"plan.docx","path":"plan-v2.docx","track":true,"author":"Li",
	  "changes":[{"replace":"综合能耗下降 8% 以上","with":"综合能耗下降 10% 以上"}]}`)
	if !rep.OK() {
		t.Fatalf("edit:\n%s", rep)
	}
	mustContain(t, rep.String(), "with tracked changes by Li", "word for word", "changed as asked")
	rep = call(ToolWord, `{"action":"check","path":"plan-v2.docx","source":"plan.docx","changes":[{"replace":"8%","with":"10%"}]}`)
	if !rep.OK() {
		t.Errorf("check against the original:\n%s", rep)
	}
	// The same check, told a different change was asked for, objects.
	rep = call(ToolWord, `{"action":"check","path":"plan-v2.docx","source":"plan.docx","changes":[{"replace":"280 万元","with":"300 万元"}]}`)
	if rep.OK() {
		t.Error("an edit other than the one asked for passed the check")
	}

	// What is wrong with a call is said, not guessed around.
	for args, want := range map[string]string{
		`{"action":"build","path":"x.xlsx"}`:  `"spec" is missing`,
		`{"action":"clean","path":"x.xlsx"}`:  `"source" is missing`,
		`{"action":"render","path":"x.xlsx"}`: `"action" must be build, clean or check`,
		`{"action":"check"}`:                  `"path" is missing`,
	} {
		if rep := call(ToolExcel, args); rep.OK() || !strings.Contains(rep.String(), want) {
			t.Errorf("%s: want %q in:\n%s", args, want, rep)
		}
	}
}
