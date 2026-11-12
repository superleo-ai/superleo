package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/liliang-cn/superleo/internal/office"
)

// `superleo doc` runs the document tools by hand: the same code the agent
// calls as word_document and excel_workbook, with the same arguments, so a
// person can make or check a file — or see what the agent would have seen —
// without starting an agent or a model.
//
//	superleo doc word  '{"action":"check","path":"plan.docx","pages":10}'
//	superleo doc excel '{"action":"check","path":"budget.xlsx"}'
//	superleo doc excel @args.json        the arguments from a file
//	superleo doc word  -                 … or from stdin
//
// Paths are relative to the current folder. The report goes to stdout; the
// exit status is 0 for "RESULT: OK" and 1 for problems.
func runDoc(args []string) int {
	usage := func() int {
		fmt.Fprintln(os.Stderr, `usage: superleo doc word|excel '<json arguments>' | @file | -
  word   {"action":"build|edit|check","path":…,"spec":{…},"source":…,"changes":[…],"track":true,"pages":N}
  excel  {"action":"build|clean|check","path":…,"spec":{…},"source":…,"key":[…],"group":[…],"sum":[…],"by_month":…}`)
		return 2
	}
	if len(args) != 2 {
		return usage()
	}
	tool, ok := map[string]string{"word": office.ToolWord, "excel": office.ToolExcel,
		office.ToolWord: office.ToolWord, office.ToolExcel: office.ToolExcel}[args[0]]
	if !ok {
		return usage()
	}
	raw := []byte(args[1])
	var err error
	switch {
	case args[1] == "-":
		raw, err = io.ReadAll(os.Stdin)
	case strings.HasPrefix(args[1], "@"):
		raw, err = os.ReadFile(args[1][1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "superleo doc:", err)
		return 2
	}
	var a map[string]any
	if err := json.Unmarshal(raw, &a); err != nil {
		fmt.Fprintln(os.Stderr, "superleo doc: the arguments are not a JSON object:", err)
		return 2
	}
	rep := office.Run(tool, a, nil)
	fmt.Print(rep.String())
	if rep.OK() {
		return 0
	}
	return 1
}
