package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/liliang-cn/superleo/internal/office"
)

// Word and Excel, as tools of the agent's own.
//
// Asked for a Word document or a workbook, the agent used to have only a
// shell: it wrote a script, in whatever language the machine happened to
// have, against whatever library it could install. On a clean Windows
// machine there is neither, and anywhere the result was only as good as that
// one script — a "total" typed in as a number, a contract edited with the
// formatting lost and no trace of what changed.
//
// These two tools are compiled into SuperLeo (internal/office): nothing to
// install, the same on macOS, Linux and Windows. Each makes a file, changes
// one, or checks one, and every answer ends in the line the other checkers
// end in — "RESULT: OK" or "RESULT: N problem(s)". A call whose report has
// problems comes back as a failed call, so the trace shows it without anyone
// opening the transcript.
//
// What they do not hold is judgement about the document — what goes in a
// project plan, when an edit should be tracked. That is in the docx and xlsx
// skills, which is where the model learns the description format too; the
// schemas here stay small on purpose, since every request carries them.

func (s *Service) registerOfficeTools() {
	resolve := func(p string) string {
		if strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				return filepath.Join(home, p[2:])
			}
		}
		root := ""
		if s.sb != nil {
			root = s.sb.Workspace()
		}
		return office.ResolveIn(root)(p)
	}
	run := func(tool string) func(context.Context, map[string]interface{}) (interface{}, error) {
		return func(_ context.Context, a map[string]interface{}) (interface{}, error) {
			rep := office.Run(tool, a, resolve)
			if rep.OK() {
				return okData(map[string]interface{}{"report": rep.String()}), nil
			}
			// A failed call, with the faults in its error: that is what the
			// model is told first and what the trace line carries.
			return map[string]interface{}{
				"ok":    false,
				"error": rep.Summary(),
				"data":  map[string]interface{}{"report": rep.String()},
			}, nil
		}
	}
	str := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	names := map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}

	s.svc.AddTool(
		office.ToolWord,
		"Make, change or check a Word document (.docx). build: write a new document from a spec (headings, contents, tables, pictures, page numbers). edit: change words in an existing document, keeping its formatting, optionally as tracked changes. check: read a document back — outline, tables, fonts, tracked changes — and compare it with the one it was made from. The spec format and when to use which are in the docx skill: call skill_docx first. Every answer ends RESULT: OK or RESULT: N problem(s).",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{"type": "string", "enum": []string{"build", "edit", "check"}},
				"path":   str("build/edit: the .docx to write. check: the .docx to check. Relative to the workspace."),
				"spec":   map[string]interface{}{"type": "object", "description": "build: the document, as the docx skill describes it"},
				"source": str("edit: the document to change (never written over). check: the original to compare with."),
				"changes": map[string]interface{}{"type": "array", "description": `edit: what to change — {"replace":"old words","with":"new words"}, {"insert_after":"words of a paragraph","text":"new paragraph"}, {"delete":"words of the paragraph to remove"}. check with source: the same list, as what was asked for.`,
					"items": map[string]interface{}{"type": "object"}},
				"track":  map[string]interface{}{"type": "boolean", "description": "edit: make the changes tracked changes (修订) the reader can accept or reject"},
				"author": str("edit with track: whose changes they are"),
				"pages":  map[string]interface{}{"type": "integer", "description": "build/check: about how many pages were asked for"},
			},
			"required": []string{"action", "path"},
		},
		run(office.ToolWord),
	)
	s.svc.AddTool(
		office.ToolExcel,
		"Make, clean or check an Excel workbook (.xlsx). build: write a workbook from a spec, with real formulas, number formats, conditional formats and charts. clean: turn a messy csv/xlsx into typed data with a formula summary and a log of every change. check: recalculate every formula without Excel and report #REF!, #DIV/0!, #NAME?, formulas that became numbers, broken chart references. The spec format and when to use which are in the xlsx skill: call skill_xlsx first. Every answer ends RESULT: OK or RESULT: N problem(s).",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action":    map[string]interface{}{"type": "string", "enum": []string{"build", "clean", "check"}},
				"path":      str("build/clean: the .xlsx to write. check: the .xlsx to check. Relative to the workspace."),
				"spec":      map[string]interface{}{"type": "object", "description": "build: the workbook, as the xlsx skill describes it"},
				"source":    str("clean: the csv or xlsx to clean. check: the original to compare rows and sums with."),
				"key":       withDesc(names, "clean: columns (by header) that identify a row; rows with the same key are duplicates. Default: the whole row."),
				"group":     withDesc(names, "clean: columns to summarise by, one block each"),
				"sum":       withDesc(names, "clean: columns to add up in the summary. Default: every numeric column."),
				"by_month":  str("clean: a date column to also summarise by month"),
				"day_first": map[string]interface{}{"type": "boolean", "description": "clean: read 03/04/2026 as 3 April when the data does not settle it"},
			},
			"required": []string{"action", "path"},
		},
		run(office.ToolExcel),
	)
}

func withDesc(schema map[string]interface{}, desc string) map[string]interface{} {
	out := map[string]interface{}{"description": desc}
	for k, v := range schema {
		out[k] = v
	}
	return out
}
