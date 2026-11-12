// Package office makes, changes and checks office documents — Word and Excel
// files — in Go, inside the SuperLeo binary.
//
// It replaced a set of Python scripts. Those needed an interpreter and a
// handful of libraries fetched at first use, which is a second runtime to
// install and keep working on every machine SuperLeo runs on, and on a clean
// Windows box there was none to start from. Everything here is compiled in:
// the same code runs on macOS, Linux and Windows with nothing installed, and
// builds with CGO off.
//
// Each format has three verbs. Build writes a new file from a JSON
// description. An editing verb changes an existing file and leaves the rest
// of it alone. Check reads a file back and says what is wrong with it. All
// three answer with a Report: the lines a person or a model reads, one
// "PROBLEM:" line per fault, and a last line that is "RESULT: OK" or
// "RESULT: N problem(s)" — the contract every checker SuperLeo ships keeps,
// so the last line of any of them means the same thing.
//
// What a format is and how to make a good one — which slide carries a chart,
// when a contract edit is tracked — is know-how, and lives in the skills
// (internal/backend/builtinskills). This package is the hands.
package office

import (
	"fmt"
	"strings"
)

// Report is what a verb has to say: the text, and the faults it found.
type Report struct {
	b        strings.Builder
	Problems []string
}

// Linef adds a line of the report.
func (r *Report) Linef(format string, args ...any) {
	fmt.Fprintf(&r.b, format, args...)
	r.b.WriteByte('\n')
}

// Problem records a fault and prints it as a "PROBLEM:" line.
func (r *Report) Problem(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.Problems = append(r.Problems, msg)
	r.b.WriteString("PROBLEM: " + msg + "\n")
}

// OK reports whether no fault was found.
func (r *Report) OK() bool { return len(r.Problems) == 0 }

// Result is the report's last line.
func (r *Report) Result() string {
	if r.OK() {
		return "RESULT: OK"
	}
	return fmt.Sprintf("RESULT: %d problem(s)", len(r.Problems))
}

// String is the whole report, ending in its result line.
func (r *Report) String() string { return r.b.String() + r.Result() + "\n" }

// Summary is the result with the faults behind it, short enough for a log
// line: what a trace shows when a check fails.
func (r *Report) Summary() string {
	if r.OK() {
		return r.Result()
	}
	shown := r.Problems
	more := ""
	if len(shown) > 3 {
		more = fmt.Sprintf(" (+%d more)", len(shown)-3)
		shown = shown[:3]
	}
	return r.Result() + ": " + strings.Join(shown, " | ") + more
}

// NotWritten is the report of a build that refused: nothing was written, and
// these are the things to fix in the description.
func NotWritten(errs []string) *Report {
	r := &Report{}
	r.Linef("NOT WRITTEN: %d thing(s) to fix in the spec", len(errs))
	for i, e := range errs {
		if i >= 40 {
			r.Linef("  ... %d more", len(errs)-40)
			break
		}
		r.Linef("  %s", e)
	}
	r.Problems = append(r.Problems, errs...)
	return r
}

func hasCJK(s string) bool {
	for _, c := range s {
		if c > 0x2E7F {
			return true
		}
	}
	return false
}

// displayWidth is how wide text is in a monospace-ish sense: wide characters
// count two.
func displayWidth(s string) int {
	n := 0
	for _, c := range s {
		if c > 0x2E7F {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
