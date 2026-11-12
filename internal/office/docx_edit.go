package office

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/beevik/etree"
)

// DocChange is one change to make in a document; exactly one of Replace,
// InsertAfter or Delete is set.
type DocChange struct {
	Replace     string `json:"replace"`      // these words…
	With        string `json:"with"`         // …become these
	InsertAfter string `json:"insert_after"` // after the paragraph containing these words…
	Text        string `json:"text"`         // …a new paragraph saying this
	Delete      string `json:"delete"`       // remove the paragraph containing these words
	All         bool   `json:"all"`          // for a replace: every occurrence, not just the one
}

// DocEditOptions says how the changes are made.
type DocEditOptions struct {
	Track  bool   // as tracked changes: the old words stay, struck through, and each change can be accepted or rejected in Word
	Author string // whose changes they are
	PDFDir string
}

// tracker stamps tracked changes with who and when.
type tracker struct {
	on     bool
	author string
	date   string
	id     int
}

func (t *tracker) mark(tag string) *etree.Element {
	t.id++
	e := etree.NewElement("w:" + tag)
	e.CreateAttr("w:id", fmt.Sprint(t.id))
	e.CreateAttr("w:author", t.author)
	e.CreateAttr("w:date", t.date)
	return e
}

// liveRuns are the runs that make up a paragraph's current text: every run
// that is not inside a tracked deletion.
func liveRuns(p *etree.Element) []*etree.Element {
	var out []*etree.Element
	walk(p, func(e *etree.Element) {
		if isW(e, "r") && changeState(e, p) != "del" {
			out = append(out, e)
		}
	})
	return out
}

func runText(r *etree.Element) string {
	var b strings.Builder
	for _, c := range r.ChildElements() {
		if isW(c, "t") {
			b.WriteString(c.Text())
		} else if isW(c, "tab") {
			b.WriteString("\t")
		}
	}
	return b.String()
}

func setRunText(r *etree.Element, text string) {
	for _, c := range r.ChildElements() {
		if isW(c, "t") || isW(c, "tab") || isW(c, "delText") {
			r.RemoveChild(c)
		}
	}
	if text != "" {
		t := r.CreateElement("w:t")
		t.CreateAttr("xml:space", "preserve")
		t.SetText(text)
	}
}

func insertAfter(ref, fresh *etree.Element) {
	parent := ref.Parent()
	parent.InsertChildAt(ref.Index()+1, fresh)
}

// splitRun cuts a run in two at a character offset, both halves keeping the
// run's formatting.
func splitRun(r *etree.Element, at int) {
	text := []rune(runText(r))
	if at <= 0 || at >= len(text) {
		return
	}
	second := r.Copy()
	setRunText(r, string(text[:at]))
	setRunText(second, string(text[at:]))
	insertAfter(r, second)
}

// isolate makes the characters [start, end) of a paragraph's current text
// occupy whole runs, and returns those runs.
func isolate(p *etree.Element, start, end int) []*etree.Element {
	pos := 0
	for _, r := range liveRuns(p) {
		n := len([]rune(runText(r)))
		if pos < start && start < pos+n {
			splitRun(r, start-pos)
		}
		pos += n
	}
	pos = 0
	for _, r := range liveRuns(p) {
		n := len([]rune(runText(r)))
		if pos < end && end < pos+n {
			splitRun(r, end-pos)
		}
		pos += n
	}
	var picked []*etree.Element
	pos = 0
	for _, r := range liveRuns(p) {
		n := len([]rune(runText(r)))
		if n > 0 && pos >= start && pos+n <= end {
			picked = append(picked, r)
		}
		pos += n
	}
	return picked
}

func toDeleted(r *etree.Element) {
	for _, c := range r.ChildElements() {
		if isW(c, "t") {
			c.Tag = "delText"
		}
	}
}

// replaceIn puts new words in place of the characters [start, end).
//
// The new words are written into a copy of the first run they replace, so
// they take its font, size, weight and colour. Untracked, the old runs are
// simply gone. Tracked, the old runs stay inside a deletion and the new run
// sits in an insertion after it.
func replaceIn(p *etree.Element, start, end int, with string, tr *tracker) bool {
	runs := isolate(p, start, end)
	if len(runs) == 0 {
		return false
	}
	if !tr.on {
		setRunText(runs[0], with)
		for _, r := range runs[1:] {
			r.Parent().RemoveChild(r)
		}
		return true
	}
	fresh := runs[0].Copy()
	setRunText(fresh, with)
	// Runs that sit side by side share one deletion; a run somewhere else in
	// the tree (inside a hyperlink, say) gets its own.
	var group *etree.Element
	for _, r := range runs {
		parent := r.Parent()
		if group == nil || group.Parent() != parent || group.Index()+1 != r.Index() {
			group = tr.mark("del")
			parent.InsertChildAt(r.Index(), group)
		}
		parent.RemoveChild(r)
		toDeleted(r)
		group.AddChild(r)
	}
	if with != "" {
		ins := tr.mark("ins")
		ins.AddChild(fresh)
		insertAfter(group, ins)
	}
	return true
}

type docHit struct {
	p  *etree.Element
	at int // rune offset in the paragraph's current text
}

func currentText(p *etree.Element) string {
	var b strings.Builder
	for _, r := range liveRuns(p) {
		b.WriteString(runText(r))
	}
	return b.String()
}

func findAll(paras []*etree.Element, needle string) []docHit {
	var hits []docHit
	for _, p := range paras {
		text := currentText(p)
		from := 0
		for {
			i := strings.Index(text[from:], needle)
			if i < 0 {
				break
			}
			hits = append(hits, docHit{p, len([]rune(text[:from+i]))})
			from += i + len(needle)
		}
	}
	return hits
}

// needOne says why a search did not come to exactly one place, or "".
func needOne(hits []docHit, what string, allowAll bool) string {
	if len(hits) == 0 {
		return fmt.Sprintf(`"%s" was not found in the document's text. Check the document to see its paragraphs; the words may be split by a line break or written differently.`, what)
	}
	if len(hits) > 1 && !allowAll {
		var where []string
		for i, h := range hits {
			if i >= 8 {
				break
			}
			where = append(where, "  "+clip(strings.TrimSpace(currentText(h.p)), 90))
		}
		return fmt.Sprintf("\"%s\" is written %d times:\n%s\nGive more of the surrounding words so it matches once, or set \"all\" to change every one.", what, len(hits), strings.Join(where, "\n"))
	}
	return ""
}

// markParagraph records on a paragraph's own mark that it was inserted or
// deleted: what makes Word show the whole paragraph, and not only its words,
// as the change.
func markParagraph(p *etree.Element, kind string, tr *tracker) {
	ppr := child(p, "pPr")
	if ppr == nil {
		ppr = etree.NewElement("w:pPr")
		p.InsertChildAt(0, ppr)
	}
	rpr := child(ppr, "rPr")
	if rpr == nil {
		rpr = ppr.CreateElement("w:rPr")
	}
	rpr.AddChild(tr.mark(kind))
}

// EditDocument changes an existing Word document and leaves everything else
// as it was, then compares the result with the original.
//
// Every search must come to exactly one place, so that nothing is changed by
// accident; the report says where the matches are when it does not.
func EditDocument(in, out string, changes []DocChange, opt DocEditOptions) *Report {
	rep := &Report{}
	if a, _ := filepath.Abs(in); a != "" {
		if b, _ := filepath.Abs(out); a == b {
			rep.Problem("the output must not be the input file: the original is needed to check the result against")
			return rep
		}
	}
	if len(changes) == 0 {
		rep.Problem("nothing to do: give at least one change")
		return rep
	}
	d, err := openDocx(in)
	if err != nil {
		rep.Problem("%s does not open as a Word document: %v", in, err)
		return rep
	}
	author := opt.Author
	if author == "" {
		author = "SuperLeo"
	}
	// Ids of tracked changes must not collide with ones already in the file.
	tr := &tracker{on: opt.Track, author: author, date: time.Now().UTC().Format("2006-01-02T15:04:05Z"), id: 9000 + len(d.trackedChanges())}

	check := DocCheckOptions{Against: in, PDFDir: opt.PDFDir}
	var notes []string
	// Find everything first: nothing is changed unless every change can be made.
	for i, c := range changes {
		n := 0
		for _, s := range []string{c.Replace, c.InsertAfter, c.Delete} {
			if s != "" {
				n++
			}
		}
		if n != 1 {
			rep.Problem(`change %d: give exactly one of "replace" (with "with"), "insert_after" (with "text") or "delete"`, i+1)
			return rep
		}
		needle := c.Replace + c.InsertAfter + c.Delete
		if msg := needOne(findAll(d.paragraphs(), needle), needle, c.All && c.Replace != ""); msg != "" {
			rep.Problem("%s", msg)
			return rep
		}
		if c.InsertAfter != "" && strings.TrimSpace(c.Text) == "" {
			rep.Problem(`change %d: "insert_after" needs "text", the new paragraph`, i+1)
			return rep
		}
	}

	for _, c := range changes {
		switch {
		case c.Replace != "":
			hits := findAll(d.paragraphs(), c.Replace)
			// Within a paragraph from the end backwards, so earlier offsets stay true.
			sort.SliceStable(hits, func(i, j int) bool { return hits[i].at > hits[j].at })
			// Only the words that differ are replaced: "费用为 USD 500" to
			// "费用为 USD 800" changes "500", in the formatting "500" has, and
			// a tracked change shows the reader exactly that.
			pre, suf := commonEnds(c.Replace, c.With)
			old, with := []rune(c.Replace), []rune(c.With)
			for _, h := range hits {
				replaceIn(h.p, h.at+pre, h.at+len(old)-suf, string(with[pre:len(with)-suf]), tr)
			}
			notes = append(notes, fmt.Sprintf(`"%s" -> "%s" (%d place(s))`, c.Replace, c.With, len(hits)))
			check.Expect = append(check.Expect, [2]string{c.Replace, c.With})
		case c.InsertAfter != "":
			p := findAll(d.paragraphs(), c.InsertAfter)[0].p
			// The new paragraph is the old one's twin: same paragraph
			// settings, same run formatting, new words.
			fresh := etree.NewElement("w:p")
			if ppr := child(p, "pPr"); ppr != nil {
				cp := ppr.Copy()
				for _, old := range cp.ChildElements() {
					if isW(old, "rPr") {
						cp.RemoveChild(old)
					}
				}
				fresh.AddChild(cp)
			}
			run := etree.NewElement("w:r")
			if runs := liveRuns(p); len(runs) > 0 {
				if rpr := child(runs[0], "rPr"); rpr != nil {
					run.AddChild(rpr.Copy())
				}
			}
			setRunText(run, c.Text)
			if tr.on {
				ins := tr.mark("ins")
				ins.AddChild(run)
				fresh.AddChild(ins)
				markParagraph(fresh, "ins", tr)
			} else {
				fresh.AddChild(run)
			}
			insertAfter(p, fresh)
			notes = append(notes, fmt.Sprintf(`new paragraph after "%s": "%s"`, clip(c.InsertAfter, 30), clip(c.Text, 60)))
			check.Allowed = append(check.Allowed, c.Text)
		case c.Delete != "":
			p := findAll(d.paragraphs(), c.Delete)[0].p
			if inTable(p) {
				rep.Problem(`"%s" is inside a table; removing a table's paragraph is not supported — replace its words instead`, c.Delete)
				return rep
			}
			whole := strings.TrimSpace(runsOfSpace.ReplaceAllString(currentText(p), " "))
			if tr.on {
				var group *etree.Element
				for _, r := range liveRuns(p) {
					parent := r.Parent()
					if group == nil || group.Parent() != parent || group.Index()+1 != r.Index() {
						group = tr.mark("del")
						parent.InsertChildAt(r.Index(), group)
					}
					parent.RemoveChild(r)
					toDeleted(r)
					group.AddChild(r)
				}
				markParagraph(p, "del", tr)
			} else {
				p.Parent().RemoveChild(p)
			}
			notes = append(notes, fmt.Sprintf(`paragraph removed: "%s"`, clip(whole, 60)))
			check.Allowed = append(check.Allowed, whole)
		}
	}

	if err := d.save(out); err != nil {
		rep.Problem("the document could not be written: %v", err)
		return rep
	}
	how := ""
	if opt.Track {
		how = " with tracked changes by " + author
	}
	rep.Linef("wrote %s%s", out, how)
	for _, n := range notes {
		rep.Linef("  %s", n)
	}
	rep.Linef("")
	rep.Linef("--- check ---")
	CheckDocumentInto(rep, out, check)
	return rep
}

// wordRune reports whether a rune is part of a Latin-script word or number:
// the runes that must not be cut apart. Everything else — spaces, punctuation,
// each Chinese character — is a place a change can start or stop.
func wordRune(r rune) bool {
	return r < 0x2E80 && (r == '_' || r == '.' || r == ',' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 0x7F)
}

// commonEnds is how many runes two strings share at the start and at the
// end, cut back so that no word or number is split: "USD 500" and "USD 800"
// share "USD " and nothing at the end, not "USD " and "00".
func commonEnds(a, b string) (prefix, suffix int) {
	x, y := []rune(a), []rune(b)
	for prefix < len(x) && prefix < len(y) && x[prefix] == y[prefix] {
		prefix++
	}
	inWord := func() bool {
		return (prefix < len(x) && wordRune(x[prefix])) || (prefix < len(y) && wordRune(y[prefix]))
	}
	for prefix > 0 && wordRune(x[prefix-1]) && inWord() {
		prefix--
	}
	for suffix < len(x)-prefix && suffix < len(y)-prefix && x[len(x)-1-suffix] == y[len(y)-1-suffix] {
		suffix++
	}
	for suffix > 0 {
		first := x[len(x)-suffix]
		beforeX := len(x)-suffix-1 >= prefix && wordRune(x[len(x)-suffix-1])
		beforeY := len(y)-suffix-1 >= prefix && wordRune(y[len(y)-suffix-1])
		if wordRune(first) && (beforeX || beforeY) {
			suffix--
			continue
		}
		break
	}
	if prefix+suffix >= len(x) && prefix+suffix >= len(y) {
		return 0, 0 // the same words: leave the whole span to the caller
	}
	return prefix, suffix
}
