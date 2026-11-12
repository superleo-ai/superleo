package office

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// CleanOptions says how a messy table is cleaned and summarised. Columns are
// named by their header text.
type CleanOptions struct {
	Sheet      string   `json:"sheet"`       // which sheet of an xlsx input; default the first
	Key        []string `json:"key"`         // rows with the same key are duplicates; default the whole row
	Group      []string `json:"group"`       // a summary block per column named here
	Sum        []string `json:"sum"`         // the columns added up; default every numeric one
	ByMonth    string   `json:"by_month"`    // also summarise by month of this date column
	DayFirst   bool     `json:"day_first"`   // read 03/04/2026 as 3 April
	MonthFirst bool     `json:"month_first"` // read it as March 4, and say so was a choice
}

// blankMarkers are what people type into a cell to say it is empty.
var blankMarkers = map[string]bool{"": true, "-": true, "--": true, "—": true, "n/a": true, "na": true,
	"null": true, "none": true, "nan": true, "#n/a": true}

var (
	spaceRe    = regexp.MustCompile(`\s+`)
	numberRe   = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	numNoise   = regexp.MustCompile(`[,\s¥￥$€£元%()]`)
	dateYMD    = regexp.MustCompile(`^(\d{4})[-/.年](\d{1,2})[-/.月](\d{1,2})日?(?:[ T]\d{1,2}:\d{2}(?::\d{2})?)?$`)
	dateTight  = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})$`)
	dateXY     = regexp.MustCompile(`^(\d{1,2})[-/.](\d{1,2})[-/.](\d{2}|\d{4})$`)
	dateDMonY  = regexp.MustCompile(`^(\d{1,2})[ -]([A-Za-z]{3})[A-Za-z]*\.?,?[ -](\d{4})$`)
	dateMonDY  = regexp.MustCompile(`^([A-Za-z]{3})[A-Za-z]*\.? (\d{1,2}),? (\d{4})$`)
	monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
)

// loadTable reads the rows of a csv/tsv or of one sheet of an xlsx as text.
func loadTable(path, sheet string) ([][]string, error) {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".xlsx") || strings.HasSuffix(lower, ".xlsm") {
		f, err := excelize.OpenFile(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if sheet == "" {
			sheet = f.GetSheetList()[0]
		}
		return f.GetRows(sheet)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := decodeText(raw)
	// The delimiter is whichever of the usual ones the first lines use most.
	head := text
	if len(head) > 4000 {
		head = head[:4000]
	}
	delim, best := ',', 0
	for _, d := range []rune{',', ';', '\t', '|'} {
		if n := strings.Count(head, string(d)); n > best {
			delim, best = d, n
		}
	}
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	return r.ReadAll()
}

// decodeText turns a text file's bytes into a string: UTF-8 (with or without
// a mark), UTF-16, else GB18030, which is what a csv saved by Excel on a
// Chinese Windows machine is.
func decodeText(raw []byte) string {
	switch {
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		return string(raw[3:])
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}), bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		big := raw[0] == 0xFE
		u := make([]uint16, 0, len(raw)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			if big {
				u = append(u, uint16(raw[i])<<8|uint16(raw[i+1]))
			} else {
				u = append(u, uint16(raw[i+1])<<8|uint16(raw[i]))
			}
		}
		return string(utf16.Decode(u))
	case utf8.Valid(raw):
		return string(raw)
	}
	if out, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw); err == nil {
		return string(out)
	}
	return string(raw)
}

func cleanCell(s string) string {
	s = strings.NewReplacer("　", " ", " ", " ").Replace(s)
	s = strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	if blankMarkers[strings.ToLower(s)] {
		return ""
	}
	return s
}

func parseNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	pct := strings.HasSuffix(s, "%")
	neg := strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")
	s = numNoise.ReplaceAllString(s, "")
	if !numberRe.MatchString(s) {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	if pct {
		n /= 100
	}
	if neg {
		n = -n
	}
	return n, true
}

// parseDate reads one cell as a date. dayFirst settles 03/04/2026.
func parseDate(s string, dayFirst bool) (time.Time, bool) {
	s = strings.TrimSpace(s)
	atoi := func(v string) int { n, _ := strconv.Atoi(v); return n }
	var y, mo, d int
	switch {
	case dateYMD.MatchString(s):
		m := dateYMD.FindStringSubmatch(s)
		y, mo, d = atoi(m[1]), atoi(m[2]), atoi(m[3])
	case dateTight.MatchString(s):
		m := dateTight.FindStringSubmatch(s)
		y, mo, d = atoi(m[1]), atoi(m[2]), atoi(m[3])
	case dateXY.MatchString(s):
		m := dateXY.FindStringSubmatch(s)
		a, b := atoi(m[1]), atoi(m[2])
		y = atoi(m[3])
		if y < 100 {
			y += 2000
		}
		if dayFirst {
			d, mo = a, b
		} else {
			mo, d = a, b
		}
	case dateDMonY.MatchString(s):
		m := dateDMonY.FindStringSubmatch(s)
		d, mo, y = atoi(m[1]), monthNames[strings.ToLower(m[2])], atoi(m[3])
	case dateMonDY.MatchString(s):
		m := dateMonDY.FindStringSubmatch(s)
		mo, d, y = monthNames[strings.ToLower(m[1])], atoi(m[2]), atoi(m[3])
	default:
		return time.Time{}, false
	}
	if mo < 1 || mo > 12 || d < 1 || d > 31 || y < 1900 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	if t.Day() != d {
		return time.Time{}, false // 31 February and the like
	}
	return t, true
}

// settleDayFirst reads whether a column's own values decide 03/04: 1 for
// day first, 0 for month first, -1 when nothing in the data says.
func settleDayFirst(vals []string) int {
	firstBig, secondBig, seen := false, false, false
	for _, v := range vals {
		m := dateXY.FindStringSubmatch(strings.TrimSpace(v))
		if m == nil {
			continue
		}
		seen = true
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if a > 12 {
			firstBig = true
		}
		if b > 12 {
			secondBig = true
		}
	}
	switch {
	case !seen:
		return 0
	case firstBig && !secondBig:
		return 1
	case secondBig && !firstBig:
		return 0
	}
	return -1
}

type logEntry struct {
	row                         int
	column, before, after, what string
}

// CleanTable cleans a messy csv or xlsx into a workbook with the cleaned
// data, a summary built from formulas over it, and a log of every change.
//
// It trims spaces, drops blank rows and columns, reads each column as what
// most of it is (date, number or text) so that mixed date spellings become
// one real date and "¥1,200.50" a number, makes spellings that differ only by
// case or spacing one spelling, and removes duplicate rows. A cell that
// cannot be read as its column's type is left as it was, marked yellow, and
// listed. The result is then checked against the input.
func CleanTable(in, out string, opt CleanOptions) *Report {
	rep := &Report{}
	if absIn, _ := filepath.Abs(in); absIn != "" {
		if absOut, _ := filepath.Abs(out); absOut == absIn {
			rep.Problem("the output must not be the input file")
			return rep
		}
	}
	table, err := loadTable(in, opt.Sheet)
	if err != nil {
		rep.Problem("%s could not be read: %v", in, err)
		return rep
	}
	start := -1
	for i, r := range table {
		if strings.TrimSpace(strings.Join(r, "")) != "" {
			start = i
			break
		}
	}
	if start < 0 {
		rep.Problem("%s has no data in it", in)
		return rep
	}
	header := make([]string, len(table[start]))
	for i, h := range table[start] {
		header[i] = cleanCell(h)
	}
	width := len(header)
	zh := hasCJK(strings.Join(header, ""))
	T := func(z, e string) string {
		if zh {
			return z
		}
		return e
	}
	var log []logEntry
	var notes []string

	type dataRow struct {
		n     int
		cells []string
	}
	var rows []dataRow
	for i, r := range table[start+1:] {
		n := start + 2 + i
		cells := make([]string, width)
		copy(cells, r)
		if len(r) > width && strings.TrimSpace(strings.Join(r[width:], "")) != "" {
			log = append(log, logEntry{n, "", strings.Join(r[width:], " | "), "", T("超出表头的单元格丢弃", "cells beyond the last header column dropped")})
		}
		rows = append(rows, dataRow{n, cells})
	}

	// Columns with no header and no data go.
	var keep []int
	for c := 0; c < width; c++ {
		used := header[c] != ""
		for _, r := range rows {
			if cleanCell(r.cells[c]) != "" {
				used = true
				break
			}
		}
		if used {
			keep = append(keep, c)
		}
	}
	droppedCols := width - len(keep)
	newHeader := make([]string, len(keep))
	seenHeader := map[string]int{}
	for i, c := range keep {
		h := header[c]
		if h == "" {
			h = fmt.Sprintf(T("列%d", "Column %d"), c+1)
		}
		seenHeader[h]++
		if seenHeader[h] > 1 {
			h = fmt.Sprintf("%s_%d", h, seenHeader[h])
		}
		newHeader[i] = h
	}
	for i := range rows {
		cells := make([]string, len(keep))
		for k, c := range keep {
			cells[k] = rows[i].cells[c]
		}
		rows[i].cells = cells
	}
	header, width = newHeader, len(newHeader)

	colIndex := func(name string) (int, bool) {
		name = strings.TrimSpace(name)
		for i, h := range header {
			if h == name || strings.EqualFold(h, name) {
				return i, true
			}
		}
		return 0, false
	}
	need := func(what string, names []string) ([]int, bool) {
		var out []int
		for _, n := range names {
			if strings.TrimSpace(n) == "" {
				continue
			}
			i, ok := colIndex(n)
			if !ok {
				rep.Problem(`%s: there is no column "%s"; the columns are: %s`, what, n, strings.Join(header, ", "))
				return nil, false
			}
			out = append(out, i)
		}
		return out, true
	}
	keys, ok1 := need("key", opt.Key)
	groups, ok2 := need("group", opt.Group)
	sums, ok3 := need("sum", opt.Sum)
	monthCol := -1
	ok4 := true
	if opt.ByMonth != "" {
		var m []int
		if m, ok4 = need("by_month", []string{opt.ByMonth}); ok4 {
			monthCol = m[0]
		}
	}
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return rep
	}

	// Trim, and blank rows out.
	totalIn := len(rows)
	blankRows, trimmed := 0, 0
	var kept []dataRow
	for _, r := range rows {
		empty := true
		for c, v := range r.cells {
			t := cleanCell(v)
			if t != v {
				trimmed++
				if strings.TrimSpace(v) != "" && t == "" {
					log = append(log, logEntry{r.n, header[c], v, "", T("空值标记清空", "blank marker cleared")})
				}
			}
			r.cells[c] = t
			if t != "" {
				empty = false
			}
		}
		if empty {
			blankRows++
			log = append(log, logEntry{r.n, "", "", "", T("空行删除", "blank row removed")})
			continue
		}
		kept = append(kept, r)
	}
	rows = kept

	// What each column is. Typed values go into parallel tables.
	kinds := make([]string, width)
	numbers := make([]map[int]float64, width) // row index -> value
	dates := make([]map[int]time.Time, width)
	unreadable := map[[2]int]bool{}
	for c := 0; c < width; c++ {
		var vals []string
		for _, r := range rows {
			if r.cells[c] != "" {
				vals = append(vals, r.cells[c])
			}
		}
		kinds[c] = "text"
		if len(vals) == 0 {
			continue
		}
		settled := settleDayFirst(vals)
		dayFirst := settled == 1 || (settled == -1 && opt.DayFirst)
		nd, nn := 0, 0
		for _, v := range vals {
			if _, ok := parseDate(v, dayFirst); ok {
				nd++
			}
			if _, ok := parseNumber(v); ok {
				nn++
			}
		}
		switch {
		case nd > 0 && float64(nd) >= 0.8*float64(len(vals)):
			kinds[c] = "date"
			dates[c] = map[int]time.Time{}
			if settled == -1 && !opt.DayFirst && !opt.MonthFirst {
				notes = append(notes, fmt.Sprintf(T(
					"列「%s」里有 03/04/2026 这样的日期，数据本身分不出是 3 月 4 日还是 4 月 3 日，按「月/日」读取；如应为「日/月」，用 day_first 重做",
					`column "%s" has dates like 03/04/2026 and nothing in the data says whether that is March 4 or April 3; they were read month-first — redo with day_first if that is wrong`), header[c]))
			}
			for i, r := range rows {
				if r.cells[c] == "" {
					continue
				}
				d, ok := parseDate(r.cells[c], dayFirst)
				if !ok {
					unreadable[[2]int{i, c}] = true
					log = append(log, logEntry{r.n, header[c], r.cells[c], r.cells[c], T("无法识别为日期，保留原值", "not readable as a date; left as it was")})
					continue
				}
				dates[c][i] = d
				if iso := d.Format("2006-01-02"); iso != r.cells[c] {
					log = append(log, logEntry{r.n, header[c], r.cells[c], iso, T("日期统一", "date normalised")})
				}
			}
		case nn > 0 && float64(nn) >= 0.8*float64(len(vals)):
			kinds[c] = "number"
			numbers[c] = map[int]float64{}
			for i, r := range rows {
				if r.cells[c] == "" {
					continue
				}
				n, ok := parseNumber(r.cells[c])
				if !ok {
					unreadable[[2]int{i, c}] = true
					log = append(log, logEntry{r.n, header[c], r.cells[c], r.cells[c], T("无法识别为数字，保留原值", "not readable as a number; left as it was")})
					continue
				}
				numbers[c][i] = n
				// "320.0" read as 320 is the same number, not a change worth a
				// line of the log; "¥8,320.00" is.
				if _, already := strconv.ParseFloat(r.cells[c], 64); already != nil {
					plain := strconv.FormatFloat(n, 'f', -1, 64)
					log = append(log, logEntry{r.n, header[c], r.cells[c], plain, T("文本转数字", "text made a number")})
				}
			}
		default:
			// Spellings that differ only by case or spacing become the commonest one.
			forms := map[string]map[string]int{}
			fold := func(s string) string { return strings.ToLower(spaceRe.ReplaceAllString(s, "")) }
			for _, r := range rows {
				if v := r.cells[c]; v != "" {
					if forms[fold(v)] == nil {
						forms[fold(v)] = map[string]int{}
					}
					forms[fold(v)][v]++
				}
			}
			for i, r := range rows {
				v := r.cells[c]
				if v == "" || len(forms[fold(v)]) < 2 {
					continue
				}
				best, bestN := "", -1
				for form, n := range forms[fold(v)] {
					if n > bestN || (n == bestN && form < best) {
						best, bestN = form, n
					}
				}
				if v != best {
					log = append(log, logEntry{r.n, header[c], v, best, T("写法统一", "spelling made consistent")})
					rows[i].cells[c] = best
				}
			}
		}
	}

	// The cell as it will be compared and written: typed where it was read.
	canon := func(i, c int) string {
		if d, ok := dates[c][i]; ok && dates[c] != nil {
			return d.Format("2006-01-02")
		}
		if n, ok := numbers[c][i]; ok && numbers[c] != nil {
			return strconv.FormatFloat(n, 'f', -1, 64)
		}
		return rows[i].cells[c]
	}

	// Duplicates.
	keyCols := keys
	if len(keyCols) == 0 {
		for c := 0; c < width; c++ {
			keyCols = append(keyCols, c)
		}
	}
	firstSeen := map[string]int{}
	var order []int // indexes into rows that survive
	dupes := 0
	for i, r := range rows {
		parts := make([]string, len(keyCols))
		for k, c := range keyCols {
			parts[k] = canon(i, c)
		}
		key := strings.Join(parts, "\x1f")
		if first, dup := firstSeen[key]; dup {
			dupes++
			all := make([]string, width)
			for c := 0; c < width; c++ {
				all[c] = r.cells[c]
			}
			log = append(log, logEntry{r.n, "", strings.Join(all, " | "), "", fmt.Sprintf(T("与第 %d 行重复，删除", "duplicate of row %d; removed"), first)})
			continue
		}
		firstSeen[key] = r.n
		order = append(order, i)
	}

	unread := 0
	stillBlank := map[string]int{}
	for _, i := range order {
		for c := 0; c < width; c++ {
			if unreadable[[2]int{i, c}] {
				unread++
			}
			if rows[i].cells[c] == "" {
				stillBlank[header[c]]++
			}
		}
	}

	// --- the workbook -------------------------------------------------------
	f := excelize.NewFile()
	defer f.Close()
	dataSheet := T("清洗后数据", "Clean")
	_ = f.SetSheetName("Sheet1", dataSheet)
	border := []excelize.Border{
		{Type: "left", Color: gridColor, Style: 1}, {Type: "right", Color: gridColor, Style: 1},
		{Type: "top", Color: gridColor, Style: 1}, {Type: "bottom", Color: gridColor, Style: 1},
	}
	mk := func(st *excelize.Style) int { id, _ := f.NewStyle(st); return id }
	nf := func(s string) *string { return &s }
	headStyle := mk(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Border: border,
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{headerFill}}, Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"}})
	textStyle := mk(&excelize.Style{Border: border})
	dateStyle := mk(&excelize.Style{Border: border, CustomNumFmt: nf("yyyy-mm-dd")})
	numStyle := mk(&excelize.Style{Border: border, CustomNumFmt: nf("#,##0.00")})
	intStyle := mk(&excelize.Style{Border: border, CustomNumFmt: nf("#,##0")})
	badStyle := mk(&excelize.Style{Border: border, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFF2CC"}}})
	boldStyle := mk(&excelize.Style{Font: &excelize.Font{Bold: true}})
	boldNum := mk(&excelize.Style{Font: &excelize.Font{Bold: true}, CustomNumFmt: nf("#,##0.00")})
	plainNum := mk(&excelize.Style{CustomNumFmt: nf("#,##0.00")})
	asText := mk(&excelize.Style{CustomNumFmt: nf("@")})
	noteStyle := mk(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "B42318"}})

	head := func(sheet string, row int, values []string) {
		for c, v := range values {
			_ = f.SetCellStr(sheet, cellName(c+1, row), v)
		}
		_ = f.SetCellStyle(sheet, cellName(1, row), cellName(len(values), row), headStyle)
	}
	head(dataSheet, 1, header)
	fractional := make([]bool, width)
	for c := 0; c < width; c++ {
		for _, n := range numbers[c] {
			if n != float64(int64(n)) {
				fractional[c] = true
				break
			}
		}
	}
	for out, i := range order {
		r := out + 2
		for c := 0; c < width; c++ {
			ref := cellName(c+1, r)
			st := textStyle
			switch {
			case unreadable[[2]int{i, c}]:
				_ = f.SetCellStr(dataSheet, ref, rows[i].cells[c])
				st = badStyle
			case rows[i].cells[c] == "":
				if kinds[c] == "date" {
					st = dateStyle
				}
			case kinds[c] == "date":
				_ = f.SetCellValue(dataSheet, ref, dates[c][i])
				st = dateStyle
			case kinds[c] == "number":
				_ = f.SetCellValue(dataSheet, ref, numbers[c][i])
				st = intStyle
				if fractional[c] {
					st = numStyle
				}
			default:
				_ = f.SetCellStr(dataSheet, ref, rows[i].cells[c])
			}
			_ = f.SetCellStyle(dataSheet, ref, ref, st)
		}
	}
	last := len(order) + 1
	for c := 0; c < width; c++ {
		longest := displayWidth(header[c])
		for k, i := range order {
			if k > 500 {
				break
			}
			if w := displayWidth(canon(i, c)); w > longest {
				longest = w
			}
		}
		w := float64(longest + 4)
		if w < 10 {
			w = 10
		}
		if w > 44 {
			w = 44
		}
		_ = f.SetColWidth(dataSheet, colName(c+1), colName(c+1), w)
	}
	_ = f.SetPanes(dataSheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	if len(order) > 0 {
		_ = f.AutoFilter(dataSheet, "A1:"+cellName(width, last), nil)
	}

	// Summary, in formulas over the clean sheet, so it follows the data.
	sumSheet := T("汇总", "Summary")
	_, _ = f.NewSheet(sumSheet)
	if len(sums) == 0 {
		for c := 0; c < width; c++ {
			if kinds[c] == "number" {
				sums = append(sums, c)
			}
		}
	}
	for _, c := range sums {
		if kinds[c] != "number" {
			rep.Problem(`sum: column "%s" is not numeric (it was read as %s)`, header[c], kinds[c])
			return rep
		}
	}
	rng := func(c int) string {
		end := last
		if end < 2 {
			end = 2
		}
		return absRange(dataSheet, c+1, 2, c+1, end)
	}
	setF := func(sheet, ref, formula string, st int) {
		_ = f.SetCellFormula(sheet, ref, formula)
		if st != 0 {
			_ = f.SetCellStyle(sheet, ref, ref, st)
		}
	}
	row := 1
	head(sumSheet, row, []string{T("指标", "Measure"), T("数值", "Value")})
	row++
	_ = f.SetCellStr(sumSheet, cellName(1, row), T("数据行数", "Rows"))
	setF(sumSheet, cellName(2, row), fmt.Sprintf("COUNTA(%s)", rng(keyCols[0])), 0)
	row++
	for _, c := range sums {
		for _, m := range [][2]string{{T("合计", "total"), "SUM"}, {T("平均", "average"), "AVERAGE"}, {T("最大", "max"), "MAX"}, {T("最小", "min"), "MIN"}} {
			_ = f.SetCellStr(sumSheet, cellName(1, row), header[c]+" "+m[0])
			setF(sumSheet, cellName(2, row), fmt.Sprintf("%s(%s)", m[1], rng(c)), plainNum)
			row++
		}
	}
	row++
	totalRow := func(top int) {
		_ = f.SetCellStr(sumSheet, cellName(1, row), T("合计", "Total"))
		_ = f.SetCellStyle(sumSheet, cellName(1, row), cellName(1, row), boldStyle)
		for k := 0; k <= len(sums); k++ {
			letter := colName(2 + k)
			st := boldNum
			if k == 0 {
				st = boldStyle
			}
			setF(sumSheet, cellName(2+k, row), fmt.Sprintf("SUM(%s%d:%s%d)", letter, top, letter, row-1), st)
		}
	}
	blockHead := func(first string) {
		h := []string{first, T("笔数", "Count")}
		for _, c := range sums {
			h = append(h, header[c])
		}
		head(sumSheet, row, h)
	}
	chartDone := false
	for _, g := range groups {
		seen := map[string]bool{}
		var values []string
		for _, i := range order {
			v := canon(i, g)
			if v != "" && !unreadable[[2]int{i, g}] && !seen[v] {
				seen[v] = true
				values = append(values, v)
			}
		}
		sort.Strings(values)
		blockHead(header[g])
		top := row + 1
		for _, v := range values {
			row++
			if n, ok := parseNumber(v); ok && kinds[g] == "number" {
				_ = f.SetCellValue(sumSheet, cellName(1, row), n)
			} else {
				_ = f.SetCellStr(sumSheet, cellName(1, row), v)
			}
			setF(sumSheet, cellName(2, row), fmt.Sprintf("COUNTIFS(%s,$A%d)", rng(g), row), 0)
			for k, c := range sums {
				setF(sumSheet, cellName(3+k, row), fmt.Sprintf("SUMIFS(%s,%s,$A%d)", rng(c), rng(g), row), plainNum)
			}
		}
		row++
		totalRow(top)
		if len(sums) > 0 && len(values) > 0 && !chartDone {
			chartDone = true
			_ = f.AddChart(sumSheet, cellName(len(sums)+5, 2), &excelize.Chart{
				Type: excelize.Col,
				Series: []excelize.ChartSeries{{
					Name:       header[sums[0]],
					Categories: absRange(sumSheet, 1, top, 1, row-1),
					Values:     absRange(sumSheet, 3, top, 3, row-1),
				}},
				Title:     excelize.ChartTitle{Paragraph: []excelize.RichTextRun{{Text: header[sums[0]] + " / " + header[g]}}},
				Dimension: excelize.ChartDimension{Width: 560, Height: 300},
				Legend:    excelize.ChartLegend{Position: "none"},
				YAxis:     excelize.ChartAxis{MajorGridLines: true},
			})
		}
		row += 2
	}
	if monthCol >= 0 {
		if kinds[monthCol] != "date" {
			rep.Problem(`by_month: column "%s" is not a date column (it was read as %s)`, header[monthCol], kinds[monthCol])
			return rep
		}
		type ym struct{ y, m int }
		seen := map[ym]bool{}
		var months []ym
		for _, i := range order {
			if d, ok := dates[monthCol][i]; ok {
				k := ym{d.Year(), int(d.Month())}
				if !seen[k] {
					seen[k] = true
					months = append(months, k)
				}
			}
		}
		sort.Slice(months, func(i, j int) bool { return months[i].y*12+months[i].m < months[j].y*12+months[j].m })
		blockHead(T("月份", "Month"))
		top := row + 1
		for _, k := range months {
			row++
			ny, nm := k.y, k.m+1
			if nm == 13 {
				ny, nm = k.y+1, 1
			}
			_ = f.SetCellStr(sumSheet, cellName(1, row), fmt.Sprintf("%04d-%02d", k.y, k.m))
			lo := fmt.Sprintf(`">="&DATE(%d,%d,1)`, k.y, k.m)
			hi := fmt.Sprintf(`"<"&DATE(%d,%d,1)`, ny, nm)
			setF(sumSheet, cellName(2, row), fmt.Sprintf("COUNTIFS(%s,%s,%s,%s)", rng(monthCol), lo, rng(monthCol), hi), 0)
			for j, c := range sums {
				setF(sumSheet, cellName(3+j, row), fmt.Sprintf("SUMIFS(%s,%s,%s,%s,%s)", rng(c), rng(monthCol), lo, rng(monthCol), hi), plainNum)
			}
		}
		row++
		totalRow(top)
		row += 2
	}
	_ = f.SetSheetDimension(dataSheet, "A1:"+cellName(width, max(last, 1)))
	// The blocks leave a gap after themselves; the sheet ends at the last one.
	if all, err := f.GetRows(sumSheet); err == nil && len(all) > 0 {
		row = len(all)
	}
	_ = f.SetSheetDimension(sumSheet, "A1:"+cellName(len(sums)+2, row))
	_ = f.SetColWidth(sumSheet, "A", "A", 24)
	_ = f.SetColWidth(sumSheet, "B", colName(len(sums)+2), 16)

	// The log.
	logSheet := T("清洗记录", "Log")
	_, _ = f.NewSheet(logSheet)
	types := make([]string, width)
	for c := range header {
		types[c] = header[c] + "=" + kinds[c]
	}
	var blanks []string
	for _, h := range header {
		if n := stillBlank[h]; n > 0 {
			blanks = append(blanks, fmt.Sprintf("%s: %d", h, n))
		}
	}
	if len(blanks) == 0 {
		blanks = []string{"0"}
	}
	facts := [][2]string{
		{T("输入文件", "Input"), filepath.Base(in)},
		{T("输入数据行", "Rows in"), strconv.Itoa(totalIn)},
		{T("删除空行", "Blank rows removed"), strconv.Itoa(blankRows)},
		{T("删除重复行", "Duplicate rows removed"), strconv.Itoa(dupes)},
		{T("输出数据行", "Rows out"), strconv.Itoa(len(order))},
		{T("删除空列", "Empty columns removed"), strconv.Itoa(droppedCols)},
		{T("去除多余空格的单元格", "Cells trimmed"), strconv.Itoa(trimmed)},
		{T("无法识别、保留原值的单元格（黄色）", "Cells left as they were, unreadable (yellow)"), strconv.Itoa(unread)},
		{T("列类型", "Column types"), strings.Join(types, ", ")},
		{T("仍为空的单元格", "Cells still blank"), strings.Join(blanks, ", ")},
	}
	lr := 1
	for _, fact := range facts {
		_ = f.SetCellStr(logSheet, cellName(1, lr), fact[0])
		_ = f.SetCellStyle(logSheet, cellName(1, lr), cellName(1, lr), boldStyle)
		if n, err := strconv.Atoi(fact[1]); err == nil {
			_ = f.SetCellValue(logSheet, cellName(2, lr), n)
		} else {
			_ = f.SetCellStr(logSheet, cellName(2, lr), fact[1])
		}
		lr++
	}
	for _, n := range notes {
		_ = f.SetCellStr(logSheet, cellName(1, lr), T("注意", "Note"))
		_ = f.SetCellStyle(logSheet, cellName(1, lr), cellName(1, lr), noteStyle)
		_ = f.SetCellStr(logSheet, cellName(2, lr), n)
		lr++
	}
	lr++
	head(logSheet, lr, []string{T("原始行号", "Input row"), T("列", "Column"), T("原值", "Before"), T("新值", "After"), T("处理", "What")})
	sort.SliceStable(log, func(i, j int) bool { return log[i].row < log[j].row })
	for _, e := range log {
		lr++
		_ = f.SetCellValue(logSheet, cellName(1, lr), e.row)
		_ = f.SetCellStr(logSheet, cellName(2, lr), e.column)
		// Kept as text on purpose: the value exactly as it was written.
		_ = f.SetCellStr(logSheet, cellName(3, lr), e.before)
		_ = f.SetCellStr(logSheet, cellName(4, lr), e.after)
		_ = f.SetCellStyle(logSheet, cellName(3, lr), cellName(4, lr), asText)
		_ = f.SetCellStr(logSheet, cellName(5, lr), e.what)
	}
	for c, w := range []float64{36, 30, 34, 22, 40} {
		_ = f.SetColWidth(logSheet, colName(c+1), colName(c+1), w)
	}

	values, _ := recalculate(f)
	full := true
	_ = f.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: &full})
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		rep.Problem("%v", err)
		return rep
	}
	if err := f.SaveAs(out); err != nil {
		rep.Problem("the workbook could not be written: %v", err)
		return rep
	}
	if _, err := cacheValues(out, values); err != nil {
		rep.Problem("the workbook could not be finished: %v", err)
		return rep
	}

	rep.Linef("wrote %s", out)
	for _, fact := range facts {
		rep.Linef("  %s: %s", fact[0], fact[1])
	}
	rep.Linef("  log entries: %d", len(log))
	if len(groups) == 0 && monthCol < 0 {
		var cats, dts []string
		for c, h := range header {
			if kinds[c] == "date" {
				dts = append(dts, h)
			}
			if kinds[c] != "text" {
				continue
			}
			distinct := map[string]bool{}
			for _, i := range order {
				if v := rows[i].cells[c]; v != "" {
					distinct[v] = true
				}
			}
			if len(distinct) > 1 && len(distinct) <= max(20, len(order)/5) {
				cats = append(cats, h)
			}
		}
		rep.Linef("  the summary holds totals only. For a pivot-style table redo with group = one of [%s]%s",
			strings.Join(cats, ", "), map[bool]string{true: " and/or by_month = one of [" + strings.Join(dts, ", ") + "]", false: ""}[len(dts) > 0])
	}
	rep.Linef("")
	rep.Linef("--- check ---")
	CheckWorkbookInto(rep, out, in)
	for _, n := range notes {
		rep.Problem("%s", n)
	}
	if unread > 0 {
		rep.Problem("%d cell(s) could not be read as their column's type and were left as they were (yellow; listed in the log sheet)", unread)
	}
	return rep
}
