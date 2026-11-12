package office

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

const budgetSpec = `{"sheets":[
 {"name":"2026预算",
  "columns":[{"header":"月份"},{"header":"收入","format":"money"},{"header":"支出","format":"money"},{"header":"结余","format":"money"},{"header":"结余率","format":"percent"}],
  "rows":[["1月",52000,41000,"=B2-C2","=D2/B2"],["2月",48000,52000,"=B3-C3","=D3/B3"],["3月",61000,45000,"=B4-C4","=D4/B4"],["4月",58000,47000,"=B5-C5","=D5/B5"]],
  "total":{"label":"合计","sum":["B","C","D"]},
  "conditional":[{"range":"D2:D5","when":"<0","fill":"red"},{"range":"B2:B5","type":"data_bar"}],
  "charts":[{"type":"column","title":"月度收支","categories":"A2:A5","series":[{"name":"B1","values":"B2:B5"},{"name":"C1","values":"C2:C5"}],"y_title":"元"}]},
 {"name":"汇总","columns":["指标",{"header":"数值","format":"money"}],
  "rows":[["全年收入","='2026预算'!B6"],["全年结余","='2026预算'!D6"],["最高月收入","=MAX('2026预算'!B2:B5)"]]}]}`

func mustContain(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("missing %q in:\n%s", w, text)
		}
	}
}

// A built workbook keeps its formulas as formulas, and carries their values
// too: the file has to show numbers where nothing calculates.
func TestBuildWorkbookKeepsFormulasAndStoresTheirValues(t *testing.T) {
	out := filepath.Join(t.TempDir(), "budget.xlsx")
	rep := BuildWorkbook([]byte(budgetSpec), out)
	if !rep.OK() {
		t.Fatalf("build reported problems:\n%s", rep)
	}
	text := rep.String()
	mustContain(t, text, "2 sheet(s), 14 formula(s), 14 with their value stored",
		"=SUM(B2:B5) → 219000", "=B3-C3 → -4000", "='2026预算'!D6 → 34000", "52,000.00",
		`chart (bar) "月度收支": 2 series`, "values '2026预算'!$B$2:$B$5: 52000, 48000, 61000, 58000",
		"frozen at A2", "formulas: 14, all recalculated here", "RESULT: OK")

	f, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if formula, _ := f.GetCellFormula("2026预算", "B6"); formula != "SUM(B2:B5)" {
		t.Errorf("the total is %q, not a formula", formula)
	}
	// The stored value, read without calculating anything.
	if v, _ := f.GetCellValue("2026预算", "B6", excelize.Options{RawCellValue: true}); v != "219000" {
		t.Errorf("no value stored beside the total's formula: %q", v)
	}
	if v, _ := f.GetCellValue("汇总", "B3", excelize.Options{RawCellValue: true}); v != "34000" {
		t.Errorf("no value stored beside a cross-sheet formula: %q", v)
	}
	conds, _ := f.GetConditionalFormats("2026预算")
	if len(conds) != 2 {
		t.Errorf("conditional formats: %d, want 2", len(conds))
	}
}

// A description that would make a broken workbook is refused, by cell, and
// nothing is written.
func TestBuildWorkbookRefusesABrokenDescription(t *testing.T) {
	out := filepath.Join(t.TempDir(), "bad.xlsx")
	rep := BuildWorkbook([]byte(`{"sheets":[{"name":"S","columns":["a","b","c"],
	  "rows":[[1,2,"=A2/0"],[1,2],[3,4,"=Nowhere!A1"]],
	  "charts":[{"type":"column","categories":"A2:A9","series":[{"values":"B2:B9"}]}],
	  "conditional":[{"range":"B2:B50","when":"<0"}]}]}`), out)
	if rep.OK() {
		t.Fatal("a broken description was accepted")
	}
	mustContain(t, rep.String(), "NOT WRITTEN", "row 3 has 2 cells for 3 columns",
		`refers to sheet "Nowhere"`, "chart categories A2:A9 reach outside the data", "conditional format range B2:B50 reaches outside")
	if _, err := os.Stat(out); err == nil {
		t.Error("a file was written although the build refused")
	}

	rep = BuildWorkbook([]byte(`{"sheets":[{"name":"S","columns":["a","b"],"rows":[[1,"=A2/0"]]}]}`), out)
	mustContain(t, rep.String(), "NOT WRITTEN", "S!B2: =A2/0 evaluates to #DIV/0!")
}

// The checker finds what goes wrong in workbooks: error values, formulas
// that became numbers, totals typed in, numbers kept as text, a chart over
// nothing — and says which formulas it could not work out instead of
// guessing at them.
func TestCheckWorkbookFindsWhatIsWrong(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "budget.xlsx")
	if rep := BuildWorkbook([]byte(budgetSpec), good); !rep.OK() {
		t.Fatalf("%s", rep)
	}
	f, err := excelize.OpenFile(good)
	if err != nil {
		t.Fatal(err)
	}
	sheet := "2026预算"
	_ = f.SetCellValue(sheet, "D4", 16000)                  // a formula became a constant
	_ = f.SetCellValue(sheet, "B6", 219000)                 // a total typed in
	_ = f.SetCellFormula(sheet, "E3", "D3/0")               // #DIV/0!
	_ = f.SetCellFormula(sheet, "F2", "SUM(Nowhere!A1:A3)") // a sheet that is not there
	_ = f.SetCellFormula(sheet, "F3", "FOOBAR(B2)")         // not a function
	_ = f.SetCellStr(sheet, "C5", "47,000")                 // a number as text
	_ = f.AddChart(sheet, "J20", &excelize.Chart{Type: excelize.Col, Series: []excelize.ChartSeries{{
		Name: "empty", Categories: "'2026预算'!$A$2:$A$5", Values: "'2026预算'!$H$2:$H$5"}}})
	bad := filepath.Join(dir, "bad.xlsx")
	if err := f.SaveAs(bad); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rep := CheckWorkbook(bad, "")
	text := rep.String()
	mustContain(t, text,
		"2026预算!E3: =D3/0 evaluates to #DIV/0!",
		`refers to sheet "Nowhere", which is not in the workbook`,
		"2026预算!D4 holds the number 16000 where the cells above and below hold a formula",
		"2026预算!B6 is the number 219000, which is exactly the sum of the 4 cells above it",
		"hold a number as text, so sums skip them: C5",
		"values ('2026预算'!$H$2:$H$5) are all empty cells",
		"RESULT: ")
	if rep.OK() {
		t.Fatal("a broken workbook passed")
	}

	// A workbook of the right shape with nothing in it: every number zero,
	// and a chart of them.
	zeros := filepath.Join(dir, "zeros.xlsx")
	empty := BuildWorkbook([]byte(`{"sheets":[{"name":"预算","columns":["月份",{"header":"收入","format":"money"}],
	  "rows":[["1月",0],["2月",0],["3月",0]],"total":{"sum":["B"]},
	  "charts":[{"type":"column","title":"收入","categories":"A2:A4","series":[{"name":"B1","values":"B2:B4"}]}]}]}`), zeros)
	mustContain(t, empty.String(), "is zero, so the chart draws nothing")
	if empty.OK() {
		t.Error("a chart of zeros passed")
	}
	// FOOBAR is either an error or said to be unchecked — never silently fine.
	if !strings.Contains(text, "2026预算!F3") {
		t.Errorf("the unknown function went unmentioned:\n%s", text)
	}
	if !strings.Contains(rep.Summary(), "RESULT: ") || !strings.Contains(rep.Summary(), "|") {
		t.Errorf("summary for the trace line: %q", rep.Summary())
	}
}

const messyCSV = "\ufeff订单号,日期,地区,销售员,产品,数量,单价,金额 \n" +
	"SO-0001,2026-03-24, 华东  ,王磊,A100 传感器,26,320.0,\"¥8,320.00\"\n" +
	"SO-0002,2026/05/18,华东,刘洋,A100 传感器,24,320.0,7680.0\n" +
	"SO-0003,2026年2月24日,西南,陈静,A100  传感器,6,320.0,1920.0\n" +
	"SO-0004,18 Jan 2026,西南,王磊,B200 控制器,2,1280.5,2561.0\n" +
	",,,,,,,\n" +
	"SO-0002,2026/05/18,华东,刘洋,A100 传感器,24,320.0,7680.0\n" +
	"SO-0005,待定,华北,,C300 网关,N/A,2499.0,\"4,998.00\"\n" +
	"SO-0006,2026/6/3,华北,李娜,C300 网关,1,2499.0,2499.0\n" +
	"SO-0007,2026-06-30,华南,李娜,A100 传感器,10,320.0,3200.0\n"

// The cleaner turns a messy export into typed data, a summary that is made
// of formulas, and a log that accounts for every row it changed or removed.
func TestCleanTable(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "sales-raw.csv")
	if err := os.WriteFile(in, []byte(messyCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "sales-clean.xlsx")
	rep := CleanTable(in, out, CleanOptions{Key: []string{"订单号"}, Group: []string{"地区"}, Sum: []string{"金额", "数量"}, ByMonth: "日期"})
	text := rep.String()
	mustContain(t, text,
		"输入数据行: 9", "删除空行: 1", "删除重复行: 1", "输出数据行: 7",
		"日期=date", "数量=number", "金额=number", "地区=text",
		"无法识别、保留原值的单元格（黄色）: 1",
		"rows: 9 in the original (8 not blank) -> 7 in sheet",
		"SUMIFS(", "COUNTIFS(",
		"1 cell(s) could not be read as their column's type")
	if rep.OK() {
		t.Error("an unreadable date must be reported, not passed over")
	}
	if len(rep.Problems) != 1 {
		t.Errorf("want exactly the one problem (the unreadable date), got %d:\n%s", len(rep.Problems), text)
	}

	f, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := f.GetSheetList(); len(got) != 3 || got[0] != "清洗后数据" || got[1] != "汇总" || got[2] != "清洗记录" {
		t.Fatalf("sheets: %v", got)
	}
	rows, _ := f.GetRows("清洗后数据", excelize.Options{RawCellValue: true})
	if rows[1][2] != "华东" || rows[3][4] != "A100 传感器" {
		t.Errorf("spaces were not cleaned: %q %q", rows[1][2], rows[3][4])
	}
	if rows[1][7] != "8320" {
		t.Errorf("¥8,320.00 did not become a number: %q", rows[1][7])
	}
	// Four spellings of a date, one real date each.
	for r := 1; r <= 4; r++ {
		if typ, _ := f.GetCellType("清洗后数据", cellName(2, r+1)); typ == excelize.CellTypeSharedString || typ == excelize.CellTypeInlineString {
			t.Errorf("row %d: the date is still text (%q)", r+1, rows[r][1])
		}
	}
	// The summary is formulas, and their values are stored.
	formula, _ := f.GetCellFormula("汇总", "B3")
	if !strings.HasPrefix(formula, "SUM(") {
		t.Errorf("the summary total is %q, not a formula", formula)
	}
	if v, _ := f.GetCellValue("汇总", "B3", excelize.Options{RawCellValue: true}); v != "31178" {
		t.Errorf("stored summary total = %q, want 31178", v)
	}
}

func TestDateAndNumberReading(t *testing.T) {
	for in, want := range map[string]string{
		"2026-03-05": "2026-03-05", "2026/3/5": "2026-03-05", "2026年3月5日": "2026-03-05", "20260305": "2026-03-05",
		"5 Mar 2026": "2026-03-05", "Mar 5, 2026": "2026-03-05", "2026-03-05 14:30": "2026-03-05",
	} {
		d, ok := parseDate(in, false)
		if !ok || d.Format("2006-01-02") != want {
			t.Errorf("parseDate(%q) = %v %v, want %s", in, d, ok, want)
		}
	}
	for _, bad := range []string{"待定", "2026-02-31", "12345", "abc", ""} {
		if _, ok := parseDate(bad, false); ok {
			t.Errorf("parseDate(%q) read a date", bad)
		}
	}
	if d, _ := parseDate("03/04/2026", true); d.Format("01-02") != "04-03" {
		t.Error("day-first not honoured")
	}
	if settleDayFirst([]string{"13/04/2026", "03/04/2026"}) != 1 || settleDayFirst([]string{"04/13/2026"}) != 0 || settleDayFirst([]string{"03/04/2026"}) != -1 {
		t.Error("settleDayFirst")
	}
	for in, want := range map[string]float64{"1,200.50": 1200.5, "¥8,320.00": 8320, "45%": 0.45, "(120)": -120, "-3": -3} {
		if n, ok := parseNumber(in); !ok || n != want {
			t.Errorf("parseNumber(%q) = %v %v", in, n, ok)
		}
	}
	if _, ok := parseNumber("SO-0001"); ok {
		t.Error("an order number was read as a number")
	}
}
