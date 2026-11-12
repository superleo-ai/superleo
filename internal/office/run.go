package office

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Running a verb from a bag of arguments.
//
// The agent reaches this package through two tools, word_document and
// excel_workbook, and a person through `superleo doc`; both hand over the
// same JSON object, and both are answered with the same Report. Keeping the
// reading of arguments here means the tool, the command and the tests cannot
// drift apart on what "source" or "changes" means.

// Tools are the names the verbs are reached under.
const (
	ToolWord  = "word_document"
	ToolExcel = "excel_workbook"
)

// Run carries out one call. resolve turns a path as the caller wrote it —
// usually relative to the agent's workspace — into one on disk.
func Run(tool string, args map[string]any, resolve func(string) string) *Report {
	if resolve == nil {
		resolve = func(p string) string { return p }
	}
	str := func(key string) string {
		s, _ := args[key].(string)
		return strings.TrimSpace(s)
	}
	path := func(key string) string {
		if p := str(key); p != "" {
			return resolve(p)
		}
		return ""
	}
	flag := func(key string) bool {
		switch v := args[key].(type) {
		case bool:
			return v
		case string:
			return strings.EqualFold(v, "true") || v == "1"
		}
		return false
	}
	list := func(key string) []string {
		switch v := args[key].(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				return nil
			}
			return strings.Split(v, ",")
		case []any:
			out := make([]string, 0, len(v))
			for _, e := range v {
				out = append(out, fmt.Sprint(e))
			}
			return out
		case []string:
			return v
		}
		return nil
	}
	fail := func(format string, a ...any) *Report {
		r := &Report{}
		r.Problem(format, a...)
		return r
	}
	// A spec arrives as an object, or — from models that serialise it
	// themselves — as that object in a string.
	spec := func() ([]byte, bool) {
		switch v := args["spec"].(type) {
		case nil:
			return nil, false
		case string:
			return []byte(v), strings.TrimSpace(v) != ""
		default:
			raw, err := json.Marshal(v)
			return raw, err == nil
		}
	}
	action := strings.ToLower(str("action"))
	target := path("path")
	if target == "" {
		return fail(`"path" is missing: the file to write, or to check`)
	}

	switch tool {
	case ToolExcel:
		switch action {
		case "build":
			raw, ok := spec()
			if !ok {
				return fail(`"spec" is missing: the description of the workbook to build`)
			}
			return BuildWorkbook(raw, target)
		case "clean":
			if path("source") == "" {
				return fail(`"source" is missing: the csv or xlsx to clean`)
			}
			return CleanTable(path("source"), target, CleanOptions{
				Sheet: str("sheet"), Key: list("key"), Group: list("group"), Sum: list("sum"),
				ByMonth: str("by_month"), DayFirst: flag("day_first"), MonthFirst: flag("month_first"),
			})
		case "check":
			return CheckWorkbook(target, path("source"))
		}
		return fail(`"action" must be build, clean or check, not "%s"`, action)

	case ToolWord:
		var changes []DocChange
		if raw, err := json.Marshal(args["changes"]); err == nil && args["changes"] != nil {
			if s, isString := args["changes"].(string); isString {
				raw = []byte(s)
			}
			if err := json.Unmarshal(raw, &changes); err != nil {
				return fail(`"changes" must be a list of {"replace","with"}, {"insert_after","text"} or {"delete"} objects: %v`, err)
			}
		}
		pages := 0
		switch v := args["pages"].(type) {
		case float64:
			pages = int(v)
		case int:
			pages = v
		case string:
			fmt.Sscan(v, &pages)
		}
		switch action {
		case "build":
			raw, ok := spec()
			if !ok {
				return fail(`"spec" is missing: the description of the document to build`)
			}
			rep := BuildDocument(raw, target, resolve("."))
			if pages > 0 && rep.OK() {
				// Built to a length: say whether it came out that long.
				again := CheckDocument(target, DocCheckOptions{Pages: pages})
				for _, p := range again.Problems {
					rep.Problem("%s", p)
				}
			}
			return rep
		case "edit":
			if path("source") == "" {
				return fail(`"source" is missing: the document to change (it is not written over; "path" is the new file)`)
			}
			return EditDocument(path("source"), target, changes, DocEditOptions{Track: flag("track"), Author: str("author"), PDFDir: path("pdf_dir")})
		case "check":
			opt := DocCheckOptions{Pages: pages, Against: path("source"), PDFDir: path("pdf_dir")}
			for _, c := range changes {
				switch {
				case c.Replace != "":
					opt.Expect = append(opt.Expect, [2]string{c.Replace, c.With})
				case c.Text != "":
					opt.Allowed = append(opt.Allowed, c.Text)
				case c.Delete != "":
					opt.Allowed = append(opt.Allowed, c.Delete)
				}
			}
			return CheckDocument(target, opt)
		}
		return fail(`"action" must be build, edit or check, not "%s"`, action)
	}
	return fail(`unknown tool "%s"`, tool)
}

// ResolveIn returns a resolver that reads relative paths inside root.
func ResolveIn(root string) func(string) string {
	return func(p string) string {
		if strings.HasPrefix(p, "~/") || filepath.IsAbs(p) || root == "" {
			return p
		}
		return filepath.Join(root, p)
	}
}
