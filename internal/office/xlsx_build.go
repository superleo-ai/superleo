package office

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// WorkbookSpec describes a workbook to build. See the xlsx skill for the
// format as the model is told it; the JSON tags are that format.
type WorkbookSpec struct {
	Sheets []SheetSpec `json:"sheets"`
}

// SheetSpec is one sheet: a header, rows of values and formulas, and what is
// laid over them.
type SheetSpec struct {
	Name        string            `json:"name"`
	Columns     []json.RawMessage `json:"columns"`
	Rows        [][]any           `json:"rows"`
	Total       *TotalSpec        `json:"total"`
	Freeze      *string           `json:"freeze"`
	Filter      *bool             `json:"filter"`
	Conditional []CondSpec        `json:"conditional"`
	Charts      []ChartSpec       `json:"charts"`
	Notes       []string          `json:"notes"`
}

// ColumnSpec is a column's header and how its cells are shown.
type ColumnSpec struct {
	Header string  `json:"header"`
	Width  float64 `json:"width"`
	Format string  `json:"format"`
}

// TotalSpec is a totals row of formulas under the data.
type TotalSpec struct {
	Label   string   `json:"label"`
	Sum     []string `json:"sum"`
	Average []string `json:"average"`
	Max     []string `json:"max"`
	Min     []string `json:"min"`
	Count   []string `json:"count"`
}

// CondSpec is a conditional format.
type CondSpec struct {
	Range string `json:"range"`
	Type  string `json:"type"`
	When  string `json:"when"`
	Fill  string `json:"fill"`
	Color string `json:"color"`
}

// ChartSpec is a chart bound to cells of its sheet.
type ChartSpec struct {
	Type       string            `json:"type"`
	Title      string            `json:"title"`
	Categories string            `json:"categories"`
	Series     []ChartSeriesSpec `json:"series"`
	Anchor     string            `json:"anchor"`
	XTitle     string            `json:"x_title"`
	YTitle     string            `json:"y_title"`
}

// ChartSeriesSpec is one series: a name (a cell holding it, or the text) and
// the cells its values are in.
type ChartSeriesSpec struct {
	Name   string `json:"name"`
	Values string `json:"values"`
}

var numberFormats = map[string]string{
	"money": "#,##0.00", "number": "#,##0.00", "int": "#,##0", "percent": "0.0%",
	"date": "yyyy-mm-dd", "text": "@",
}

// condFills are the fill and ink of a highlighted cell, by name.
var condFills = map[string][2]string{
	"red": {"FDE2E1", "B42318"}, "green": {"DCFAE6", "067647"},
	"yellow": {"FEF0C7", "93370D"}, "blue": {"D9E8FF", "1D4ED8"},
}

const (
	headerFill = "1F4E79"
	gridColor  = "D0D5DD"
)

var (
	badSheetName = regexp.MustCompile(`[\[\]:*?/\\]`)
	cellRefRe    = regexp.MustCompile(`^\$?[A-Za-z]{1,3}\$?\d+$`)
	sheetRefRe   = regexp.MustCompile(`(?:'([^']+)'|([A-Za-z_\x{4e00}-\x{9fff}][\w\x{4e00}-\x{9fff}.]*))!`)
	quotedRe     = regexp.MustCompile(`"[^"]*"`)
	compareRe    = regexp.MustCompile(`^(<=|>=|<>|!=|<|>|=)\s*(.+)$`)
	isoDateRe    = regexp.MustCompile(`^(\d{4})[-/](\d{1,2})[-/](\d{1,2})$`)
)

func isFormula(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(s, "=") && len(s) > 1
}

func cellName(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}

func colName(col int) string {
	name, _ := excelize.ColumnNumberToName(col)
	return name
}

// parseRange reads "A2:B13", "$A$2:$B$13" or "'Sheet'!A2:B13".
func parseRange(ref string) (sheet string, c1, r1, c2, r2 int, err error) {
	ref = strings.TrimSpace(ref)
	if i := strings.LastIndex(ref, "!"); i >= 0 {
		sheet = strings.ReplaceAll(strings.Trim(ref[:i], "'"), "''", "'")
		ref = ref[i+1:]
	}
	ref = strings.ReplaceAll(ref, "$", "")
	parts := strings.Split(ref, ":")
	if len(parts) > 2 || ref == "" {
		return "", 0, 0, 0, 0, fmt.Errorf("%q is not a range", ref)
	}
	if c1, r1, err = excelize.CellNameToCoordinates(parts[0]); err != nil {
		return "", 0, 0, 0, 0, fmt.Errorf("%q is not a range", ref)
	}
	c2, r2 = c1, r1
	if len(parts) == 2 {
		if c2, r2, err = excelize.CellNameToCoordinates(parts[1]); err != nil {
			return "", 0, 0, 0, 0, fmt.Errorf("%q is not a range", ref)
		}
	}
	return sheet, c1, r1, c2, r2, nil
}

func quoteSheet(name string) string {
	return "'" + strings.ReplaceAll(name, "'", "''") + "'"
}

func absRange(sheet string, c1, r1, c2, r2 int) string {
	a, _ := excelize.CoordinatesToCellName(c1, r1, true)
	b, _ := excelize.CoordinatesToCellName(c2, r2, true)
	if a == b {
		return quoteSheet(sheet) + "!" + a
	}
	return quoteSheet(sheet) + "!" + a + ":" + b
}

// BuildWorkbook writes a workbook from its description.
//
// Formulas stay formulas, numbers carry number formats, the header is frozen
// and filterable, charts are bound to their cells. Every formula is then
// worked out and its value stored beside it, so the numbers also show where
// nothing calculates — Quick Look, a phone's preview, a mail client.
//
// It refuses, by sheet and cell, what would be a broken workbook: nothing is
// written until the list is empty.
func BuildWorkbook(specJSON []byte, out string) *Report {
	var spec WorkbookSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return NotWritten([]string{"the spec is not valid JSON: " + err.Error()})
	}
	if len(spec.Sheets) == 0 {
		return NotWritten([]string{`the spec has no "sheets"`})
	}
	var errs []string
	bad := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	names := make([]string, len(spec.Sheets))
	seen := map[string]bool{}
	for i, s := range spec.Sheets {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			name = fmt.Sprintf("Sheet%d", i+1)
		}
		if len([]rune(name)) > 31 || badSheetName.MatchString(name) {
			bad(`sheet name "%s": at most 31 characters and none of [ ] : * ? / \`, name)
		}
		if seen[strings.ToUpper(name)] {
			bad(`two sheets are named "%s"`, name)
		}
		seen[strings.ToUpper(name)] = true
		names[i] = name
	}
	if len(errs) > 0 {
		return NotWritten(errs)
	}

	f := excelize.NewFile()
	defer f.Close()
	style := func(st *excelize.Style) int {
		id, err := f.NewStyle(st)
		if err != nil {
			bad("a cell style could not be made: %v", err)
		}
		return id
	}
	border := []excelize.Border{
		{Type: "left", Color: gridColor, Style: 1}, {Type: "right", Color: gridColor, Style: 1},
		{Type: "top", Color: gridColor, Style: 1}, {Type: "bottom", Color: gridColor, Style: 1},
	}
	headStyle := style(&excelize.Style{
		Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Border: border,
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{headerFill}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	})
	noteStyle := style(&excelize.Style{Font: &excelize.Font{Italic: true, Color: "667085"}})
	bodyStyles := map[string]int{}
	bodyStyle := func(format string, total bool) int {
		key := fmt.Sprintf("%s|%v", format, total)
		if id, ok := bodyStyles[key]; ok {
			return id
		}
		st := &excelize.Style{Border: border}
		if format != "" {
			nf := format
			if known, ok := numberFormats[format]; ok {
				nf = known
			}
			st.CustomNumFmt = &nf
		}
		if total {
			st.Font = &excelize.Font{Bold: true}
			st.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"EEF2F7"}}
			st.Border = []excelize.Border{
				{Type: "left", Color: gridColor, Style: 1}, {Type: "right", Color: gridColor, Style: 1},
				{Type: "top", Color: "667085", Style: 2}, {Type: "bottom", Color: gridColor, Style: 1},
			}
		}
		bodyStyles[key] = style(st)
		return bodyStyles[key]
	}

	type extent struct{ cols, last int }
	extents := map[string]extent{}
	formulas := 0

	for si, s := range spec.Sheets {
		name := names[si]
		if si == 0 {
			_ = f.SetSheetName("Sheet1", name)
		} else if _, err := f.NewSheet(name); err != nil {
			bad("%s: %v", name, err)
			continue
		}
		cols := make([]ColumnSpec, len(s.Columns))
		for i, raw := range s.Columns {
			// A column is {"header": …} or just its header text.
			if err := json.Unmarshal(raw, &cols[i]); err != nil {
				var header string
				if json.Unmarshal(raw, &header) != nil {
					bad(`%s: column %d is neither a header text nor {"header": …}`, name, i+1)
				}
				cols[i] = ColumnSpec{Header: header}
			}
		}
		if len(cols) == 0 {
			bad(`%s: no "columns"`, name)
			continue
		}
		for ci, col := range cols {
			_ = f.SetCellValue(name, cellName(ci+1, 1), col.Header)
		}
		_ = f.SetCellStyle(name, "A1", cellName(len(cols), 1), headStyle)
		_ = f.SetRowHeight(name, 1, 22)

		setCell := func(col, row int, v any, format string, total bool) {
			ref := cellName(col, row)
			switch val := v.(type) {
			case nil:
			case string:
				switch {
				case isFormula(val):
					formulas++
					if err := f.SetCellFormula(name, ref, strings.TrimPrefix(val, "=")); err != nil {
						bad("%s!%s: %v", name, ref, err)
					}
				case format == "date" && isoDateRe.MatchString(strings.TrimSpace(val)):
					m := isoDateRe.FindStringSubmatch(strings.TrimSpace(val))
					var y, mo, d int
					fmt.Sscan(m[1], &y)
					fmt.Sscan(m[2], &mo)
					fmt.Sscan(m[3], &d)
					_ = f.SetCellValue(name, ref, time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC))
				default:
					_ = f.SetCellStr(name, ref, val)
				}
			default:
				_ = f.SetCellValue(name, ref, val)
			}
			_ = f.SetCellStyle(name, ref, ref, bodyStyle(format, total))
		}

		for ri, row := range s.Rows {
			if len(row) != len(cols) {
				bad("%s: row %d has %d cells for %d columns", name, ri+2, len(row), len(cols))
				continue
			}
			for ci, v := range row {
				setCell(ci+1, ri+2, v, cols[ci].Format, false)
			}
		}
		last := len(s.Rows) + 1
		if s.Total != nil && len(s.Rows) > 0 {
			tr := last + 1
			label := s.Total.Label
			if label == "" {
				label = "Total"
				if hasCJK(name + cols[0].Header) {
					label = "合计"
				}
			}
			for ci := range cols {
				setCell(ci+1, tr, nil, cols[ci].Format, true)
			}
			_ = f.SetCellStr(name, cellName(1, tr), label)
			for _, group := range []struct {
				fn      string
				letters []string
			}{{"SUM", s.Total.Sum}, {"AVERAGE", s.Total.Average}, {"MAX", s.Total.Max}, {"MIN", s.Total.Min}, {"COUNT", s.Total.Count}} {
				for _, letter := range group.letters {
					letter = strings.ToUpper(strings.TrimSpace(letter))
					ci, err := excelize.ColumnNameToNumber(letter)
					if err != nil {
						bad(`%s: total column "%s" is not a column letter`, name, letter)
						continue
					}
					if ci > len(cols) || ci == 1 {
						bad("%s: total column %s is outside the table, or is the label column", name, letter)
						continue
					}
					setCell(ci, tr, fmt.Sprintf("=%s(%s2:%s%d)", group.fn, letter, letter, last), cols[ci-1].Format, true)
				}
			}
			last = tr
		}
		extents[strings.ToUpper(name)] = extent{len(cols), last}
		_ = f.SetSheetDimension(name, "A1:"+cellName(len(cols), last+notesRows(len(s.Notes))))

		// Widths: what was asked for, else from the longest thing in the
		// column — and for numbers, from the total, which is longer than any
		// of them: a column too narrow shows ##### instead.
		for ci, col := range cols {
			width := col.Width
			if width <= 0 {
				longest := displayWidth(col.Header) + 2
				sum := 0.0
				for _, row := range s.Rows {
					if ci >= len(row) {
						continue
					}
					switch v := row[ci].(type) {
					case string:
						if !isFormula(v) && displayWidth(v) > longest {
							longest = displayWidth(v)
						}
					case float64:
						if v < 0 {
							sum -= v
						} else {
							sum += v
						}
					}
				}
				width = float64(longest + 3)
				if col.Format != "" && col.Format != "text" && col.Format != "date" {
					if sum == 0 {
						sum = 1000000
					}
					if w := float64(len(fmt.Sprintf("%.2f", sum))*4/3 + 4); w > width {
						width = w
					}
				}
				if width < 10 {
					width = 10
				}
				if width > 48 {
					width = 48
				}
			}
			_ = f.SetColWidth(name, colName(ci+1), colName(ci+1), width)
		}

		freeze := "A2"
		if s.Freeze != nil {
			freeze = strings.ToUpper(strings.TrimSpace(*s.Freeze))
		}
		if freeze != "" {
			if c, r, err := excelize.CellNameToCoordinates(freeze); err == nil && (c > 1 || r > 1) {
				pane := "bottomLeft"
				if c > 1 && r > 1 {
					pane = "bottomRight"
				} else if c > 1 {
					pane = "topRight"
				}
				_ = f.SetPanes(name, &excelize.Panes{Freeze: true, XSplit: c - 1, YSplit: r - 1, TopLeftCell: freeze, ActivePane: pane})
			} else if err != nil {
				bad(`%s: "freeze" must be a cell like "A2"`, name)
			}
		}
		if (s.Filter == nil || *s.Filter) && len(s.Rows) > 0 {
			_ = f.AutoFilter(name, "A1:"+cellName(len(cols), len(s.Rows)+1), nil)
		}

		for _, rule := range s.Conditional {
			_, _, _, c2, r2, err := parseRange(rule.Range)
			if err != nil {
				bad(`%s: conditional format range "%s" is not a range`, name, rule.Range)
				continue
			}
			if r2 > last || c2 > len(cols) {
				bad("%s: conditional format range %s reaches outside the data (%s)", name, rule.Range, cellName(len(cols), last))
				continue
			}
			ref := strings.ReplaceAll(rule.Range, "$", "")
			var opts excelize.ConditionalFormatOptions
			switch strings.ToLower(rule.Type) {
			case "data_bar":
				color := strings.TrimPrefix(rule.Color, "#")
				if color == "" {
					color = "5B8FF9"
				}
				opts = excelize.ConditionalFormatOptions{Type: "data_bar", Criteria: "=", MinType: "min", MaxType: "max", BarColor: "#" + color}
			case "color_scale":
				opts = excelize.ConditionalFormatOptions{Type: "3_color_scale", Criteria: "=", MinType: "min", MidType: "percentile",
					MidValue: "50", MaxType: "max", MinColor: "#F8696B", MidColor: "#FFEB84", MaxColor: "#63BE7B"}
			default:
				fill, ok := condFills[strings.ToLower(rule.Fill)]
				if !ok {
					if rule.Fill == "" {
						fill = condFills["red"]
					} else {
						fill = [2]string{strings.TrimPrefix(rule.Fill, "#"), "111827"}
					}
				}
				id, cerr := f.NewConditionalStyle(&excelize.Style{
					Font: &excelize.Font{Color: fill[1]},
					Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{fill[0]}},
				})
				if cerr != nil {
					bad("%s: conditional format on %s: %v", name, rule.Range, cerr)
					continue
				}
				when := strings.TrimSpace(rule.When)
				m := compareRe.FindStringSubmatch(when)
				switch {
				case strings.HasPrefix(when, "=") && (m == nil || strings.ContainsAny(m[2], "()")):
					opts = excelize.ConditionalFormatOptions{Type: "formula", Criteria: strings.TrimPrefix(when, "="), Format: &id}
				case m != nil:
					op := m[1]
					if op == "!=" {
						op = "<>"
					}
					opts = excelize.ConditionalFormatOptions{Type: "cell", Criteria: op, Value: m[2], Format: &id}
				default:
					bad(`%s: conditional format on %s needs "when": a comparison like "<0", or a formula "=…"`, name, rule.Range)
					continue
				}
			}
			if err := f.SetConditionalFormat(name, ref, []excelize.ConditionalFormatOptions{opts}); err != nil {
				bad("%s: conditional format on %s: %v", name, rule.Range, err)
			}
		}

		for k, text := range s.Notes {
			ref := cellName(1, last+2+k)
			_ = f.SetCellStr(name, ref, text)
			_ = f.SetCellStyle(name, ref, ref, noteStyle)
		}

		for _, ch := range s.Charts {
			kinds := map[string]excelize.ChartType{"column": excelize.Col, "bar": excelize.Bar, "line": excelize.Line,
				"pie": excelize.Pie, "area": excelize.Area}
			kind, ok := kinds[strings.ToLower(ch.Type)]
			if ch.Type == "" {
				kind, ok = excelize.Col, true
			}
			if !ok {
				bad(`%s: unknown chart type "%s" (column, bar, line, pie, area)`, name, ch.Type)
				continue
			}
			_, cc1, cr1, cc2, cr2, err := parseRange(ch.Categories)
			if err != nil {
				bad(`%s: a chart needs "categories": a range like "A2:A13"`, name)
				continue
			}
			if cr2 > last || cc2 > len(cols) {
				bad("%s: chart categories %s reach outside the data", name, ch.Categories)
				continue
			}
			var series []excelize.ChartSeries
			good := true
			for _, sr := range ch.Series {
				_, c1, r1, c2, r2, err := parseRange(sr.Values)
				if err != nil {
					bad(`%s: a chart series needs "values": a range like "B2:B13"`, name)
					good = false
					continue
				}
				if r2 > last || c2 > len(cols) {
					bad("%s: chart series %s reaches outside the data", name, sr.Values)
					good = false
					continue
				}
				one := excelize.ChartSeries{
					Categories: absRange(name, cc1, cr1, cc2, cr2),
					Values:     absRange(name, c1, r1, c2, r2),
				}
				if cellRefRe.MatchString(sr.Name) {
					nc, nr, _ := excelize.CellNameToCoordinates(strings.ReplaceAll(sr.Name, "$", ""))
					one.Name = absRange(name, nc, nr, nc, nr)
				} else {
					one.Name = sr.Name
				}
				series = append(series, one)
			}
			if len(series) == 0 {
				bad("%s: a chart has no series", name)
				continue
			}
			if !good {
				continue
			}
			anchor := ch.Anchor
			if anchor == "" {
				anchor = cellName(len(cols)+2, 2)
			}
			chart := &excelize.Chart{
				Type: kind, Series: series,
				Dimension: excelize.ChartDimension{Width: 620, Height: 330},
				Legend:    excelize.ChartLegend{Position: "bottom"},
				PlotArea:  excelize.ChartPlotArea{ShowVal: kind == excelize.Pie},
			}
			if ch.Title != "" {
				chart.Title = excelize.ChartTitle{Paragraph: []excelize.RichTextRun{{Text: ch.Title}}}
			}
			if kind != excelize.Pie {
				chart.YAxis.MajorGridLines = true
				if ch.YTitle != "" {
					chart.YAxis.Title = excelize.ChartTitle{Paragraph: []excelize.RichTextRun{{Text: ch.YTitle}}}
				}
				if ch.XTitle != "" {
					chart.XAxis.Title = excelize.ChartTitle{Paragraph: []excelize.RichTextRun{{Text: ch.XTitle}}}
				}
			}
			if err := f.AddChart(name, anchor, chart); err != nil {
				bad("%s: the chart could not be added: %v", name, err)
			}
		}
	}

	// Every formula's sheet references land on a sheet that exists.
	for _, name := range f.GetSheetList() {
		ext := extents[strings.ToUpper(name)]
		for r := 2; r <= ext.last; r++ {
			for c := 1; c <= ext.cols; c++ {
				formula, _ := f.GetCellFormula(name, cellName(c, r))
				if formula == "" {
					continue
				}
				for _, m := range sheetRefRe.FindAllStringSubmatch(quotedRe.ReplaceAllString(formula, `""`), -1) {
					target := m[1] + m[2]
					if _, ok := extents[strings.ToUpper(target)]; !ok {
						bad(`%s!%s: the formula refers to sheet "%s", which is not in this workbook`, name, cellName(c, r), target)
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		return NotWritten(errs)
	}

	// Work every formula out. One that comes to an error is a broken workbook.
	values, notEvaluated := recalculate(f)
	for _, name := range f.GetSheetList() {
		ext := extents[strings.ToUpper(name)]
		for r := 2; r <= ext.last; r++ {
			for c := 1; c <= ext.cols; c++ {
				ref := cellName(c, r)
				if v, ok := values[cellKey{name, ref}]; ok && isErrorValue(v) {
					formula, _ := f.GetCellFormula(name, ref)
					bad("%s!%s: =%s evaluates to %s", name, ref, formula, v)
				}
			}
		}
	}
	if len(errs) > 0 {
		return NotWritten(errs)
	}
	full := true
	_ = f.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: &full})
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return NotWritten([]string{err.Error()})
	}
	tmp := out + ".building.xlsx"
	if err := f.SaveAs(tmp); err != nil {
		return NotWritten([]string{"the workbook could not be written: " + err.Error()})
	}
	cached, err := cacheValues(tmp, values)
	if err != nil {
		os.Remove(tmp)
		return NotWritten([]string{"the workbook could not be finished: " + err.Error()})
	}
	if err := os.Rename(tmp, out); err != nil {
		os.Remove(tmp)
		return NotWritten([]string{err.Error()})
	}

	rep := &Report{}
	rep.Linef("wrote %s: %d sheet(s), %d formula(s), %d with their value stored", out, len(names), formulas, cached)
	if len(notEvaluated) > 0 {
		rep.Linef("note: %d formula(s) could not be worked out here (a function this evaluator does not know); Excel works them out when it opens the file", len(notEvaluated))
	}
	rep.Linef("")
	rep.Linef("--- check ---")
	CheckWorkbookInto(rep, out, "")
	return rep
}

// notesRows is how many rows the notes under a table take, the gap included.
func notesRows(n int) int {
	if n == 0 {
		return 0
	}
	return n + 1
}
