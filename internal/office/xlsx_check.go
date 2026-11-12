package office

import (
	"archive/zip"
	"fmt"
	"html"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// showRows is how many rows of a sheet the report prints from the top.
const showRows = 14

var (
	textNumberRe = regexp.MustCompile(`^\s*-?[\d,]+(\.\d+)?\s*$`)
	refInFormula = regexp.MustCompile(`(\$?)\b([A-Za-z]{1,3})(\$?)(\d+)\b`)
	// A chart's XML names its elements with a "c:" prefix or with none,
	// depending on which program wrote it.
	chartSerRe   = regexp.MustCompile(`(?s)<(?:c:)?ser>.*?</(?:c:)?ser>`)
	chartValRe   = regexp.MustCompile(`(?s)<(?:c:)?val>.*?<(?:c:)?f>(.*?)</(?:c:)?f>`)
	chartCatRe   = regexp.MustCompile(`(?s)<(?:c:)?cat>.*?<(?:c:)?f>(.*?)</(?:c:)?f>`)
	chartTitleRe = regexp.MustCompile(`(?s)<(?:c:)?title>.*?</(?:c:)?title>`)
	chartTextRe  = regexp.MustCompile(`<a:t>([^<]*)</a:t>`)
	chartKindRe  = regexp.MustCompile(`<(?:c:)?(bar|line|pie|area|scatter|doughnut|radar|bubble)Chart>`)
)

// CheckWorkbook reads a workbook back, works its formulas out, and says what
// is wrong with it. against, when given, is the csv or xlsx it was made from:
// rows and column sums are compared, so data lost in a clean-up shows.
func CheckWorkbook(path, against string) *Report {
	rep := &Report{}
	CheckWorkbookInto(rep, path, against)
	return rep
}

// relativeFormula rewrites a formula's references relative to its own cell,
// so two cells holding "the same formula, filled down" compare equal.
func relativeFormula(formula string, col, row int) string {
	bare := quotedRe.ReplaceAllString(formula, `""`)
	return refInFormula.ReplaceAllStringFunc(bare, func(m string) string {
		p := refInFormula.FindStringSubmatch(m)
		ci, err := excelize.ColumnNameToNumber(strings.ToUpper(p[2]))
		if err != nil {
			return m
		}
		ri, _ := strconv.Atoi(p[4])
		c := fmt.Sprintf("C[%d]", ci-col)
		if p[1] != "" {
			c = fmt.Sprintf("C%d", ci)
		}
		r := fmt.Sprintf("R[%d]", ri-row)
		if p[3] != "" {
			r = fmt.Sprintf("R%d", ri)
		}
		return c + r
	})
}

func sameValue(a, b string) bool {
	if a == b {
		return true
	}
	x, err1 := strconv.ParseFloat(a, 64)
	y, err2 := strconv.ParseFloat(b, 64)
	if err1 != nil || err2 != nil {
		return false
	}
	return math.Abs(x-y) <= 1e-6*math.Max(1, math.Max(math.Abs(x), math.Abs(y)))
}

// chartInfo is one chart as its XML describes it.
type chartInfo struct {
	kind, title string
	series      [][2]string // values ref, categories ref
}

// readCharts pulls every chart's bindings out of the file. The workbook
// library writes charts and has no way to read them back.
func readCharts(path string) []chartInfo {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil
	}
	defer zr.Close()
	var names []string
	files := map[string]*zip.File{}
	for _, zf := range zr.File {
		if strings.HasPrefix(zf.Name, "xl/charts/chart") && strings.HasSuffix(zf.Name, ".xml") {
			names = append(names, zf.Name)
			files[zf.Name] = zf
		}
	}
	sort.Strings(names)
	var out []chartInfo
	for _, name := range names {
		rc, err := files[name].Open()
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(rc)
		rc.Close()
		xml := string(raw)
		info := chartInfo{}
		if m := chartKindRe.FindStringSubmatch(xml); m != nil {
			info.kind = m[1]
		}
		if t := chartTitleRe.FindString(xml); t != "" {
			for _, m := range chartTextRe.FindAllStringSubmatch(t, -1) {
				info.title += m[1]
			}
		}
		for _, ser := range chartSerRe.FindAllString(xml, -1) {
			var pair [2]string
			if m := chartValRe.FindStringSubmatch(ser); m != nil {
				pair[0] = htmlUnescape(m[1])
			}
			if m := chartCatRe.FindStringSubmatch(ser); m != nil {
				pair[1] = htmlUnescape(m[1])
			}
			info.series = append(info.series, pair)
		}
		out = append(out, info)
	}
	return out
}

func htmlUnescape(s string) string { return html.UnescapeString(s) }

// CheckWorkbookInto is CheckWorkbook writing into a report that already has
// lines in it: a builder ends with the check of what it built.
func CheckWorkbookInto(rep *Report, path, against string) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		rep.Problem("%s does not open as a workbook: %v", path, err)
		return
	}
	defer f.Close()
	sheets := f.GetSheetList()
	exists := map[string]string{}
	for _, s := range sheets {
		exists[strings.ToUpper(s)] = s
	}
	rep.Linef("%s: %d sheet(s): %s", path, len(sheets), strings.Join(sheets, ", "))
	values, notEvaluated := recalculate(f)

	raw := func(sheet, ref string) string {
		v, _ := f.GetCellValue(sheet, ref, excelize.Options{RawCellValue: true})
		return v
	}
	// value is what a cell comes to: its formula worked out, else what it holds.
	value := func(sheet, ref string) (string, bool) {
		if v, ok := values[cellKey{sheet, ref}]; ok {
			return v, true
		}
		if _, bad := notEvaluated[cellKey{sheet, ref}]; bad {
			return "", false
		}
		return raw(sheet, ref), true
	}
	isNumber := func(sheet, ref string) (float64, bool) {
		if t, _ := f.GetCellType(sheet, ref); t == excelize.CellTypeSharedString || t == excelize.CellTypeInlineString {
			return 0, false
		}
		v, ok := value(sheet, ref)
		if !ok || v == "" {
			return 0, false
		}
		n, err := strconv.ParseFloat(v, 64)
		return n, err == nil
	}

	totalFormulas := 0
	for _, sheet := range sheets {
		cols, rows := sheetExtent(f, sheet)
		formulas := formulaCells(f, sheet)
		totalFormulas += len(formulas)
		frozen, filter := "-", "-"
		if p, err := f.GetPanes(sheet); err == nil && p.Freeze {
			frozen = p.TopLeftCell
		}
		conds, _ := f.GetConditionalFormats(sheet)
		rep.Linef("")
		rep.Linef("[%s] %d row(s) x %d column(s), %d formula(s), frozen at %s, %d conditional format(s)", sheet, rows, cols, len(formulas), frozen, len(conds))
		_ = filter
		if rows <= 1 && cols <= 1 && raw(sheet, "A1") == "" {
			rep.Problem(`sheet "%s" is empty`, sheet)
			continue
		}

		for r := 1; r <= rows; r++ {
			if r > showRows && r <= rows-2 {
				if r == showRows+1 {
					rep.Linef("      | ... %d more row(s)", rows-showRows-2)
				}
				continue
			}
			cells := make([]string, cols)
			for c := 1; c <= cols; c++ {
				ref := cellName(c, r)
				if formula, ok := formulas[ref]; ok {
					if v, ok := value(sheet, ref); ok {
						cells[c-1] = "=" + formula + " → " + formatNumber(v)
					} else {
						cells[c-1] = "=" + formula + " → ?"
					}
				} else {
					// As the sheet shows it: a date as a date, not its serial number.
					shown, _ := f.GetCellValue(sheet, ref)
					cells[c-1] = shown
				}
			}
			rep.Linef("  %3d | %s", r, clip(strings.Join(cells, " | "), 230))
		}

		// Formulas: errors, stored values gone stale, sheets that are not there.
		refs := make([]string, 0, len(formulas))
		for ref := range formulas {
			refs = append(refs, ref)
		}
		sort.Slice(refs, func(i, j int) bool {
			ci, ri, _ := excelize.CellNameToCoordinates(refs[i])
			cj, rj, _ := excelize.CellNameToCoordinates(refs[j])
			if ri != rj {
				return ri < rj
			}
			return ci < cj
		})
		for _, ref := range refs {
			formula := formulas[ref]
			where := sheet + "!" + ref
			for _, m := range sheetRefRe.FindAllStringSubmatch(quotedRe.ReplaceAllString(formula, `""`), -1) {
				target := m[1] + m[2]
				if _, ok := exists[strings.ToUpper(target)]; !ok && !strings.HasPrefix(target, "[") {
					rep.Problem(`%s: =%s refers to sheet "%s", which is not in the workbook`, where, formula, target)
				}
			}
			if strings.Contains(formula, "#REF!") {
				rep.Problem("%s: the formula itself contains #REF! — a cell it pointed to was deleted: =%s", where, formula)
				continue
			}
			v, ok := values[cellKey{sheet, ref}]
			if !ok {
				continue
			}
			if isErrorValue(v) {
				rep.Problem("%s: =%s evaluates to %s", where, formula, v)
				continue
			}
			if stored := raw(sheet, ref); stored != "" && !sameValue(stored, v) {
				rep.Problem("%s: the value stored in the file (%s) is not what =%s works out to (%s)", where, formatNumber(stored), formula, formatNumber(v))
			}
		}

		// A number where the column is formulas: a formula pasted as its value.
		for c := 1; c <= cols; c++ {
			groups := map[string][]int{}
			for r := 2; r <= rows; r++ {
				if formula, ok := formulas[cellName(c, r)]; ok {
					key := relativeFormula(formula, c, r)
					groups[key] = append(groups[key], r)
				}
			}
			var members []int
			for _, g := range groups {
				if len(g) > len(members) {
					members = g
				}
			}
			if len(members) < 3 {
				continue
			}
			for r := members[0]; r <= members[len(members)-1]; r++ {
				ref := cellName(c, r)
				if _, isF := formulas[ref]; isF {
					continue
				}
				if n, ok := isNumber(sheet, ref); ok {
					rep.Problem("%s!%s holds the number %s where the cells above and below hold a formula (=%s): a formula became a constant",
						sheet, ref, formatNumber(strconv.FormatFloat(n, 'f', -1, 64)), formulas[cellName(c, members[0])])
				}
			}
		}

		// A total typed in: a number that is exactly the sum of the numbers above it.
		for c := 2; c <= cols; c++ {
			var run []float64
			for r := 2; r <= rows; r++ {
				ref := cellName(c, r)
				n, ok := isNumber(sheet, ref)
				if !ok {
					run = nil
					continue
				}
				if _, isF := formulas[ref]; !isF && len(run) >= 3 && n != 0 {
					sum := 0.0
					for _, v := range run {
						sum += v
					}
					label := raw(sheet, cellName(1, r))
					_, labelIsNumber := isNumber(sheet, cellName(1, r))
					if math.Abs(sum-n) < 1e-6*math.Max(1, math.Abs(n)) && strings.TrimSpace(label) != "" && !labelIsNumber {
						rep.Problem("%s!%s is the number %s, which is exactly the sum of the %d cells above it: a total typed in as a number instead of =SUM(...)",
							sheet, ref, formatNumber(strconv.FormatFloat(n, 'f', -1, 64)), len(run))
					}
				}
				run = append(run, n)
			}
		}

		// Error values stored outright, and numbers kept as text.
		var textNumbers []string
		for r := 2; r <= rows && r <= maxCells/max(cols, 1); r++ {
			for c := 1; c <= cols; c++ {
				ref := cellName(c, r)
				if _, isF := formulas[ref]; isF {
					continue
				}
				v := raw(sheet, ref)
				if v == "" {
					continue
				}
				if isErrorValue(v) {
					rep.Problem("%s!%s holds the error value %s", sheet, ref, v)
					continue
				}
				t, _ := f.GetCellType(sheet, ref)
				if (t == excelize.CellTypeSharedString || t == excelize.CellTypeInlineString) && textNumberRe.MatchString(v) &&
					len(strings.TrimSpace(v)) < 15 && !strings.HasPrefix(strings.TrimSpace(v), "0") && !isTextFormatted(f, sheet, ref) {
					textNumbers = append(textNumbers, ref)
				}
			}
		}
		if len(textNumbers) > 0 {
			shown := textNumbers
			more := ""
			if len(shown) > 8 {
				shown, more = shown[:8], " ..."
			}
			rep.Problem(`sheet "%s": %d cell(s) hold a number as text, so sums skip them: %s%s`, sheet, len(textNumbers), strings.Join(shown, ", "), more)
		}

		for ref := range conds {
			for _, part := range strings.Fields(ref) {
				_, c1, r1, c2, r2, err := parseRange(part)
				if err != nil {
					continue
				}
				empty := true
				for r := r1; r <= r2 && r <= r1+200 && empty; r++ {
					for c := c1; c <= c2; c++ {
						if raw(sheet, cellName(c, r)) != "" || formulas[cellName(c, r)] != "" {
							empty = false
							break
						}
					}
				}
				if empty {
					rep.Problem(`sheet "%s": the conditional format on %s covers only empty cells`, sheet, part)
				}
			}
			kinds := []string{}
			for _, o := range conds[ref] {
				kinds = append(kinds, o.Type)
			}
			rep.Linef("  conditional format on %s: %s", ref, strings.Join(kinds, ", "))
		}
	}

	// Charts, from the file's own XML.
	charts := readCharts(path)
	if len(charts) > 0 {
		rep.Linef("")
	}
	for _, ch := range charts {
		rep.Linef(`chart (%s) "%s": %d series`, ch.kind, ch.title, len(ch.series))
		if len(ch.series) == 0 {
			rep.Problem(`chart "%s" has no series`, ch.title)
		}
		for _, pair := range ch.series {
			if pair[0] == "" {
				rep.Problem(`chart "%s" has a series that is bound to no cells`, ch.title)
			}
			for k, ref := range pair {
				if ref == "" {
					continue
				}
				what := []string{"values", "categories"}[k]
				sheet, c1, r1, c2, r2, err := parseRange(ref)
				if err != nil {
					rep.Problem(`chart "%s": %s reference "%s" is not a range`, ch.title, what, ref)
					continue
				}
				real, ok := exists[strings.ToUpper(sheet)]
				if !ok {
					rep.Problem(`chart "%s": %s point at sheet "%s", which is not in the workbook`, ch.title, what, sheet)
					continue
				}
				var got []string
				filled, numbers, nonZero := 0, 0, 0
				for r := r1; r <= r2; r++ {
					for c := c1; c <= c2; c++ {
						v, _ := value(real, cellName(c, r))
						if v != "" {
							filled++
						}
						if n, ok := isNumber(real, cellName(c, r)); ok {
							numbers++
							if n != 0 {
								nonZero++
							}
						}
						if len(got) < 14 {
							got = append(got, formatNumber(v))
						}
					}
				}
				rep.Linef("  %s %s: %s", what, ref, strings.Join(got, ", "))
				if filled == 0 {
					rep.Problem(`chart "%s": %s (%s) are all empty cells`, ch.title, what, ref)
				} else if k == 0 && numbers == 0 {
					rep.Problem(`chart "%s": values (%s) hold no numbers`, ch.title, ref)
				} else if k == 0 && nonZero == 0 {
					rep.Problem(`chart "%s": every value in %s is zero, so the chart draws nothing`, ch.title, ref)
				}
			}
		}
	}

	rep.Linef("")
	if len(notEvaluated) == 0 {
		rep.Linef("formulas: %d, all recalculated here", totalFormulas)
	} else {
		rep.Linef("formulas: %d, recalculated here except %d", totalFormulas, len(notEvaluated))
		rep.Linef("not evaluated here (%d) — this evaluator does not know the function; these are UNCHECKED, not wrong, and Excel works them out on open:", len(notEvaluated))
		keys := make([]cellKey, 0, len(notEvaluated))
		for k := range notEvaluated {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].sheet+keys[i].ref < keys[j].sheet+keys[j].ref })
		for i, k := range keys {
			if i >= 12 {
				rep.Linef("  ... %d more", len(keys)-12)
				break
			}
			formula, _ := f.GetCellFormula(k.sheet, k.ref)
			rep.Linef("  %s!%s =%s (%s)", k.sheet, k.ref, clip(formula, 60), clip(notEvaluated[k], 80))
		}
	}
	rep.Linef("not checked: how the workbook looks on screen (column widths, colours, the chart as drawn); that needs Excel or a person")

	if against != "" {
		compareWorkbook(rep, f, sheets[0], against, value)
	}
}

// isTextFormatted reports whether a cell is formatted as text on purpose.
func isTextFormatted(f *excelize.File, sheet, ref string) bool {
	id, err := f.GetCellStyle(sheet, ref)
	if err != nil {
		return false
	}
	st, err := f.GetStyle(id)
	if err != nil || st == nil {
		return false
	}
	return st.NumFmt == 49 || (st.CustomNumFmt != nil && *st.CustomNumFmt == "@")
}

var moneyNoise = regexp.MustCompile(`[,\s¥￥$€£元]`)

func toNumber(s string) (float64, bool) {
	s = moneyNoise.ReplaceAllString(s, "")
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	return n, err == nil
}

// compareWorkbook sets a workbook's first sheet against the table it was
// made from: how many rows, and the sum of every numeric column.
func compareWorkbook(rep *Report, f *excelize.File, sheet, against string, value func(string, string) (string, bool)) {
	rep.Linef("")
	rep.Linef("--- compared with %s ---", against)
	orig, err := loadTable(against, "")
	if err != nil {
		rep.Problem("the original could not be read: %v", err)
		return
	}
	if len(orig) == 0 {
		return
	}
	header := make([]string, len(orig[0]))
	for i, h := range orig[0] {
		header[i] = strings.TrimSpace(h)
	}
	var body [][]string
	for _, r := range orig[1:] {
		if strings.TrimSpace(strings.Join(r, "")) != "" {
			body = append(body, r)
		}
	}
	cols, rows := sheetExtent(f, sheet)
	newHeader := make([]string, cols)
	for c := 1; c <= cols; c++ {
		v, _ := value(sheet, cellName(c, 1))
		newHeader[c-1] = strings.TrimSpace(v)
	}
	newRows := 0
	for r := 2; r <= rows; r++ {
		for c := 1; c <= cols; c++ {
			if v, _ := value(sheet, cellName(c, r)); v != "" {
				newRows++
				break
			}
		}
	}
	rep.Linef(`rows: %d in the original (%d not blank) -> %d in sheet "%s"`, len(orig)-1, len(body), newRows, sheet)
	index := func(name string) int {
		for i, h := range newHeader {
			if h == name {
				return i
			}
		}
		return -1
	}
	for ci, h := range header {
		if h == "" {
			continue
		}
		ni := index(h)
		if ni < 0 {
			rep.Problem(`column "%s" of the original is not in sheet "%s"`, h, sheet)
			continue
		}
		sum, n := 0.0, 0
		for _, r := range body {
			if ci < len(r) {
				if v, ok := toNumber(r[ci]); ok {
					sum += v
					n++
				}
			}
		}
		if n < 3 || n < len(body)/2 {
			continue
		}
		newSum, newN := 0.0, 0
		for r := 2; r <= rows; r++ {
			v, _ := value(sheet, cellName(ni+1, r))
			if x, ok := toNumber(v); ok {
				newSum += x
				newN++
			}
		}
		rep.Linef("  %s: sum %s over %d row(s) -> %s over %d row(s)", h, formatNumber(strconv.FormatFloat(sum, 'f', 4, 64)), n,
			formatNumber(strconv.FormatFloat(newSum, 'f', 4, 64)), newN)
	}
	rep.Linef("Rows and sums that differ must be accounted for by what was removed (duplicates, blanks): the log sheet lists each one.")
}
