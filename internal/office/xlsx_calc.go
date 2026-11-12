package office

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Working a workbook's formulas out without Excel.
//
// A workbook written by a program holds formulas and no results. Excel fills
// the results in when it opens the file; nothing else does, so a check that
// only reads the file sees every formula as an empty cell and can say nothing
// about #REF! or #DIV/0!. The evaluator here is excelize's own: it knows a
// few hundred worksheet functions. A formula using one it does not know is
// reported as "not evaluated here" — a gap in the check, said out loud — and
// never as an error in the workbook.

type cellKey struct{ sheet, ref string }

var errorValues = []string{"#REF!", "#DIV/0!", "#NAME?", "#VALUE!", "#N/A", "#NUM!", "#NULL!"}

func isErrorValue(v string) bool {
	for _, e := range errorValues {
		if v == e {
			return true
		}
	}
	return false
}

// maxCells bounds how much of one sheet is walked cell by cell.
const maxCells = 400000

// sheetExtent is the last column and row a sheet uses: the larger of what
// the sheet declares and what its rows hold. Neither alone is enough — a file
// written by a library often declares "A1" whatever is in it, and reading the
// rows drops trailing cells whose formula has no stored value.
func sheetExtent(f *excelize.File, sheet string) (cols, rows int) {
	if dim, err := f.GetSheetDimension(sheet); err == nil && dim != "" {
		if _, _, _, c2, r2, err := parseRange(dim); err == nil {
			cols, rows = c2, r2
		}
	}
	if all, err := f.GetRows(sheet); err == nil {
		if len(all) > rows {
			rows = len(all)
		}
		for _, r := range all {
			if len(r) > cols {
				cols = len(r)
			}
		}
	}
	return cols, rows
}

// formulaCells lists every cell of a sheet that holds a formula.
func formulaCells(f *excelize.File, sheet string) map[string]string {
	out := map[string]string{}
	cols, rows := sheetExtent(f, sheet)
	if cols*rows > maxCells {
		rows = maxCells / max(cols, 1)
	}
	for r := 1; r <= rows; r++ {
		for c := 1; c <= cols; c++ {
			ref := cellName(c, r)
			if formula, _ := f.GetCellFormula(sheet, ref); formula != "" {
				out[ref] = formula
			}
		}
	}
	return out
}

// recalculate works out every formula. values holds the result of each one
// that could be worked out (an error value such as #DIV/0! is a result);
// notEvaluated holds, for the rest, why not.
func recalculate(f *excelize.File) (values map[cellKey]string, notEvaluated map[cellKey]string) {
	values, notEvaluated = map[cellKey]string{}, map[cellKey]string{}
	for _, sheet := range f.GetSheetList() {
		for ref := range formulaCells(f, sheet) {
			res, err := f.CalcCellValue(sheet, ref, excelize.Options{RawCellValue: true})
			switch {
			case isErrorValue(res):
				values[cellKey{sheet, ref}] = res
			case err != nil && isErrorValue(strings.TrimSpace(err.Error())):
				values[cellKey{sheet, ref}] = strings.TrimSpace(err.Error())
			case err != nil:
				notEvaluated[cellKey{sheet, ref}] = err.Error()
			default:
				values[cellKey{sheet, ref}] = res
			}
		}
	}
	return values, notEvaluated
}

var (
	sheetTagRe = regexp.MustCompile(`<sheet\b[^>]*>`)
	attrRe     = func(name string) *regexp.Regexp { return regexp.MustCompile(`\b` + name + `="([^"]*)"`) }
	nameAttr   = attrRe("name")
	ridAttr    = regexp.MustCompile(`\br:id="([^"]*)"`)
	cellRe     = regexp.MustCompile(`(?s)<c\b([^>]*[^/])>(.*?)</c>`)
	cellRefAt  = regexp.MustCompile(`\br="([A-Z]+\d+)"`)
	cellTypeAt = regexp.MustCompile(`\s+t="[^"]*"`)
	valueRe    = regexp.MustCompile(`(?s)<v>.*?</v>|<v/>`)
)

// sheetFiles maps each worksheet's path inside the xlsx to its name.
func sheetFiles(zr *zip.Reader) (map[string]string, error) {
	read := func(name string) (string, error) {
		for _, zf := range zr.File {
			if zf.Name == name {
				rc, err := zf.Open()
				if err != nil {
					return "", err
				}
				defer rc.Close()
				b, err := io.ReadAll(rc)
				return string(b), err
			}
		}
		return "", fmt.Errorf("%s is not in the file", name)
	}
	book, err := read("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	rels, err := read("xl/_rels/workbook.xml.rels")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, tag := range sheetTagRe.FindAllString(book, -1) {
		name, rid := nameAttr.FindStringSubmatch(tag), ridAttr.FindStringSubmatch(tag)
		if name == nil || rid == nil {
			continue
		}
		rel := regexp.MustCompile(`<Relationship\b[^>]*\bId="` + regexp.QuoteMeta(rid[1]) + `"[^>]*>`).FindString(rels)
		target := attrRe("Target").FindStringSubmatch(rel)
		if target == nil {
			continue
		}
		path := strings.TrimPrefix(target[1], "/")
		if !strings.HasPrefix(path, "xl/") {
			path = "xl/" + path
		}
		out[path] = html.UnescapeString(name[1])
	}
	return out, nil
}

// cacheValues stores the worked-out value beside every formula in the file,
// as Excel itself would have saved it.
//
// Anything that only shows a workbook — Quick Look, a phone's preview, a
// mail client — shows a formula with no stored value as an empty cell. The
// library that writes the workbook has no way to store one, so it is put into
// the sheet's XML here. Returns how many formula cells got a value.
func cacheValues(path string, values map[cellKey]string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return 0, err
	}
	sheets, err := sheetFiles(zr)
	if err != nil {
		return 0, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	count := 0
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			return 0, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return 0, err
		}
		if sheet, ok := sheets[zf.Name]; ok {
			data = []byte(cellRe.ReplaceAllStringFunc(string(data), func(cell string) string {
				m := cellRe.FindStringSubmatch(cell)
				attrs, inner := m[1], m[2]
				if !strings.Contains(inner, "<f") {
					return cell
				}
				ref := cellRefAt.FindStringSubmatch(attrs)
				if ref == nil {
					return cell
				}
				val, ok := values[cellKey{sheet, ref[1]}]
				if !ok || val == "" {
					return cell
				}
				inner = valueRe.ReplaceAllString(inner, "")
				attrs = cellTypeAt.ReplaceAllString(attrs, "")
				body := html.EscapeString(val)
				switch {
				case isErrorValue(val):
					attrs += ` t="e"`
				case val == "TRUE" || val == "FALSE":
					attrs += ` t="b"`
					body = map[string]string{"TRUE": "1", "FALSE": "0"}[val]
				default:
					if _, err := strconv.ParseFloat(val, 64); err != nil {
						attrs += ` t="str"`
					}
				}
				count++
				return "<c" + attrs + ">" + inner + "<v>" + body + "</v></c>"
			}))
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: zf.Name, Method: zip.Deflate, Modified: zf.Modified})
		if err != nil {
			return 0, err
		}
		if _, err := w.Write(data); err != nil {
			return 0, err
		}
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return 0, err
	}
	// The library saves workbooks executable; a document is not a program.
	return count, os.Chmod(path, 0o644)
}

// formatNumber prints a number the way a person reads it in a report.
func formatNumber(s string) string {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	if v == float64(int64(v)) && v < 1e15 && v > -1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	out := strconv.FormatFloat(v, 'f', 4, 64)
	return strings.TrimRight(strings.TrimRight(out, "0"), ".")
}
