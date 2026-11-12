package office

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/beevik/etree"
)

// DocCheckOptions says what a document is checked against.
type DocCheckOptions struct {
	Pages   int         // about how many pages were asked for; 0 = not checked
	Against string      // the document this one was made from
	Expect  [][2]string // the changes asked for, old -> new; anything else that changed is a problem
	// Allowed are paragraphs that were asked to be added or removed.
	Allowed []string
	PDFDir  string // keep the laid-out PDF here, when LibreOffice is there to make one
}

var (
	instrRe     = regexp.MustCompile(`<w:instrText[^>]*>([^<]*)</w:instrText>`)
	fldSimpleRe = regexp.MustCompile(`w:instr="([^"]*)"`)
	tagRe       = regexp.MustCompile(`<[^>]+>`)
	headerPart  = regexp.MustCompile(`^word/header\d*\.xml$`)
	footerPart  = regexp.MustCompile(`^word/footer\d*\.xml$`)
	paraRe      = regexp.MustCompile(`(?s)<w:p[ >].*?</w:p>`)
)

// partText is the visible text of a header or footer part.
func partText(xml string) string {
	var lines []string
	for _, p := range paraRe.FindAllString(xml, -1) {
		p = instrRe.ReplaceAllString(p, "")
		if t := strings.TrimSpace(htmlUnescape(tagRe.ReplaceAllString(p, ""))); t != "" {
			lines = append(lines, t)
		}
	}
	return strings.Join(lines, " / ")
}

func fieldCodes(xml string) string {
	var codes []string
	for _, m := range instrRe.FindAllStringSubmatch(xml, -1) {
		codes = append(codes, m[1])
	}
	for _, m := range fldSimpleRe.FindAllStringSubmatch(xml, -1) {
		codes = append(codes, m[1])
	}
	return strings.Join(codes, " ")
}

var pageFieldRe = regexp.MustCompile(`(^|\s)PAGE(\s|$)`)

// CheckDocument reads a Word document back and says what is wrong with it.
func CheckDocument(path string, opt DocCheckOptions) *Report {
	rep := &Report{}
	CheckDocumentInto(rep, path, opt)
	return rep
}

// CheckDocumentInto is CheckDocument writing into a report that already has
// lines in it.
func CheckDocumentInto(rep *Report, path string, opt DocCheckOptions) {
	d, err := openDocx(path)
	if err != nil {
		rep.Problem("%s does not open as a Word document: %v", path, err)
		return
	}
	paras := d.paragraphs()
	chars, withText := 0, 0
	for _, p := range paras {
		if t := strings.TrimSpace(paraText(p, viewAccepted)); t != "" {
			withText++
			chars += len([]rune(t))
		}
	}
	var tables []*etree.Element
	pictures := 0
	walk(d.body, func(e *etree.Element) {
		if isW(e, "tbl") {
			tables = append(tables, e)
		}
		if isW(e, "drawing") || isW(e, "pict") {
			pictures++
		}
	})
	rep.Linef("%s: %d paragraph(s) with text, %d character(s), %d table(s), %d picture(s)", path, withText, chars, len(tables), pictures)
	if chars == 0 && len(tables) == 0 && pictures == 0 {
		rep.Problem("the document is empty")
		return
	}

	var headerText, footerText []string
	pageField := false
	for _, p := range d.partsLike(headerPart) {
		if t := partText(string(p.data)); t != "" {
			headerText = append(headerText, t)
		}
	}
	for _, p := range d.partsLike(footerPart) {
		if t := partText(string(p.data)); t != "" {
			footerText = append(footerText, t)
		}
		if pageFieldRe.MatchString(fieldCodes(string(p.data))) {
			pageField = true
		}
	}
	orDash := func(s []string) string {
		if len(s) == 0 {
			return "-"
		}
		return strings.Join(s, " / ")
	}
	rep.Linef("header: %s", orDash(headerText))
	if pageField {
		rep.Linef("footer: %s  [page number field]", orDash(footerText))
	} else {
		rep.Linef("footer: %s", orDash(footerText))
	}
	hasTOC := strings.Contains(fieldCodes(string(d.part("word/document.xml"))), "TOC")
	rep.Linef("table of contents: %s", map[bool]string{true: "a TOC field", false: "none"}[hasTOC])

	// Outline.
	rep.Linef("")
	rep.Linef("outline:")
	type heading struct {
		level int
		text  string
	}
	var headings []heading
	var fake, tocEntries []string
	prevLevel, since := 0, 0
	lastHeading := ""
	inTOC, depth := false, 0
	for _, p := range paras {
		text := paraText(p, viewAccepted)
		// The contents are a field that can span many paragraphs, with other
		// fields (page references) nested inside: follow its depth.
		begins, ends, startsTOC := 0, 0, false
		walk(p, func(e *etree.Element) {
			if isW(e, "fldChar") {
				switch attr(e, "fldCharType") {
				case "begin":
					begins++
				case "end":
					ends++
				}
			}
			if isW(e, "instrText") && strings.Contains(e.Text(), "TOC") {
				startsTOC = true
			}
		})
		if startsTOC && !inTOC {
			inTOC, depth = true, 0
		}
		if inTOC {
			depth += begins - ends
			if t := strings.TrimSpace(text); t != "" {
				if i := strings.Index(t, "\t"); i >= 0 {
					t = strings.TrimSpace(t[:i])
				}
				tocEntries = append(tocEntries, t)
			}
			if depth <= 0 {
				inTOC = false
			}
			continue
		}
		stripped := strings.TrimSpace(text)
		if inTable(p) {
			// A table is content: a heading with only a table under it is
			// not an empty section.
			since += len([]rune(stripped))
			continue
		}
		if stripped == "" {
			// …and so is a picture.
			walk(p, func(e *etree.Element) {
				if isW(e, "drawing") || isW(e, "pict") {
					since++
				}
			})
		}
		if level := d.headingLevel(p); level > 0 {
			if lastHeading != "" && since == 0 && level <= prevLevel {
				rep.Problem(`the heading "%s" has nothing under it`, clip(lastHeading, 50))
			}
			if level > prevLevel+1 && len(headings) > 0 {
				rep.Problem(`the heading "%s" is level %d right after level %d: a level was skipped`, clip(stripped, 40), level, prevLevel)
			}
			if stripped == "" {
				rep.Problem("an empty heading (level %d)", level)
			}
			headings = append(headings, heading{level, stripped})
			rep.Linef("  %s%s  [%s]", strings.Repeat("  ", level-1), clip(stripped, 80), d.styleName(p))
			prevLevel, since, lastHeading = level, 0, stripped
			continue
		}
		if stripped == "" {
			continue
		}
		since += len([]rune(stripped))
		// Looks like a heading, is not one: a short line, all bold, in body style.
		if len([]rune(stripped)) <= 40 && !strings.ContainsAny(stripped[len(stripped)-1:], ".;:,") &&
			!strings.HasSuffix(stripped, "。") && !strings.HasSuffix(stripped, "；") && !strings.HasSuffix(stripped, "：") && !strings.HasSuffix(stripped, "，") &&
			attr(child(child(p, "pPr"), "jc"), "val") != "center" && d.styleID(p) == d.defaultPara {
			allBold, any := true, false
			walk(p, func(e *etree.Element) {
				if !isW(e, "r") {
					return
				}
				has := false
				for _, t := range e.ChildElements() {
					if isW(t, "t") && strings.TrimSpace(t.Text()) != "" {
						has = true
					}
				}
				if !has {
					return
				}
				any = true
				b := child(child(e, "rPr"), "b")
				if b == nil || attr(b, "val") == "0" || attr(b, "val") == "false" {
					allBold = false
				}
			})
			if any && allBold {
				fake = append(fake, stripped)
			}
		}
	}
	if lastHeading != "" && since == 0 && len(headings) > 1 {
		rep.Problem(`the heading "%s" has nothing under it`, clip(lastHeading, 50))
	}
	if len(headings) == 0 {
		rep.Linef("  (no headings)")
	}
	if len(fake) >= 3 && len(fake) > len(headings) {
		rep.Problem(`%d paragraph(s) are bold lines standing in for headings ("%s", …) and are not heading styles: Word's navigation and a table of contents cannot see them`, len(fake), clip(fake[0], 30))
	}

	if hasTOC {
		listed := func(h string) bool {
			for _, e := range tocEntries {
				if e != "" && (e == h || strings.Contains(e, h) || strings.Contains(h, e)) {
					return true
				}
			}
			return false
		}
		var missing []string
		for _, h := range headings {
			if !listed(h.text) {
				missing = append(missing, h.text)
			}
		}
		switch {
		case len(tocEntries) == 0:
			rep.Linef("note: the contents field has no entries stored; Word fills it in when the file is opened and the field updated")
		case len(missing) > 0:
			rep.Problem(`the table of contents does not list %d heading(s) the document has, e.g. "%s"`, len(missing), clip(missing[0], 40))
		default:
			rep.Linef("the table of contents lists all %d heading(s)", len(headings))
		}
		for _, e := range tocEntries {
			found := false
			for _, h := range headings {
				if e == h.text || strings.Contains(e, h.text) || strings.Contains(h.text, e) {
					found = true
					break
				}
			}
			if !found && len(headings) > 0 {
				rep.Problem(`the table of contents lists "%s", which is not a heading in the document`, clip(e, 40))
				break
			}
		}
	}

	// Fonts for Chinese text.
	undeclared := 0
	east := map[string]int{}
	for _, p := range paras {
		if !hasCJK(paraText(p, viewAccepted)) {
			continue
		}
		walk(p, func(e *etree.Element) {
			if !isW(e, "r") {
				return
			}
			var text strings.Builder
			for _, t := range e.ChildElements() {
				if isW(t, "t") {
					text.WriteString(t.Text())
				}
			}
			if !hasCJK(text.String()) {
				return
			}
			if f := d.eastFont(p, e); f == "" {
				undeclared++
			} else {
				east[f]++
			}
		})
	}
	if len(east) > 0 {
		names := make([]string, 0, len(east))
		for f := range east {
			names = append(names, f)
		}
		sort.Slice(names, func(i, j int) bool { return east[names[i]] > east[names[j]] })
		for i, f := range names {
			names[i] = fmt.Sprintf("%s (%d run(s))", f, east[f])
		}
		rep.Linef("")
		rep.Linef("fonts for Chinese text: %s", strings.Join(names, ", "))
	}
	if undeclared > 0 {
		rep.Problem("%d run(s) of Chinese text name no East Asian font — not on the run, its style, or the document defaults; each program will choose its own", undeclared)
	}

	// Tables.
	for n, tbl := range tables {
		var rows [][]string
		for _, tr := range tbl.ChildElements() {
			if !isW(tr, "tr") {
				continue
			}
			var cells []string
			for _, tc := range tr.ChildElements() {
				if !isW(tc, "tc") {
					continue
				}
				var parts []string
				walk(tc, func(e *etree.Element) {
					if isW(e, "p") {
						if t := strings.TrimSpace(paraText(e, viewAccepted)); t != "" {
							parts = append(parts, t)
						}
					}
				})
				cells = append(cells, strings.Join(parts, " "))
			}
			rows = append(rows, cells)
		}
		cols := 0
		if len(rows) > 0 {
			cols = len(rows[0])
		}
		rep.Linef("")
		rep.Linef("table %d: %d row(s) x %d column(s)", n+1, len(rows), cols)
		for i, r := range rows {
			if i >= 6 {
				rep.Linef("  | ... %d more row(s)", len(rows)-6)
				break
			}
			short := make([]string, len(r))
			for k, v := range r {
				short[k] = clip(v, 24)
			}
			rep.Linef("  | %s", strings.Join(short, " | "))
		}
		if len(rows) <= 1 {
			rep.Problem("table %d has a header row and no data", n+1)
			continue
		}
		empty, total := 0, 0
		for _, r := range rows[1:] {
			for _, v := range r {
				total++
				if v == "" {
					empty++
				}
			}
		}
		if total > 3 && float64(empty) > 0.4*float64(total) {
			rep.Problem("table %d: %d of %d cells under the header are empty", n+1, empty, total)
		}
	}

	// Pictures really in the file.
	rels := string(d.part("word/_rels/document.xml.rels"))
	for _, m := range regexp.MustCompile(`r:embed="([^"]+)"`).FindAllStringSubmatch(string(d.part("word/document.xml")), -1) {
		rel := regexp.MustCompile(`<Relationship\b[^>]*\bId="` + regexp.QuoteMeta(m[1]) + `"[^>]*>`).FindString(rels)
		target := attrRe("Target").FindStringSubmatch(rel)
		if target == nil || d.part("word/"+strings.TrimPrefix(target[1], "/word/")) == nil {
			rep.Problem("a picture points at a file that is not in the document")
		}
	}

	if changes := d.trackedChanges(); len(changes) > 0 {
		rep.Linef("")
		rep.Linef("tracked changes: %d", len(changes))
		for i, c := range changes {
			if i >= 40 {
				rep.Linef("  ... %d more", len(changes)-40)
				break
			}
			author := c.author
			if author == "" {
				author = "?"
			}
			date := ""
			if len(c.date) >= 10 {
				date = " " + c.date[:10]
			}
			rep.Linef("  %s by %s%s: %s", c.kind, author, date, clip(c.text, 90))
		}
	}

	// Pages.
	rep.Linef("")
	pages, pdf, real := layOut(path, opt.PDFDir)
	if real {
		rep.Linef("pages: %d (laid out by LibreOffice; Word may differ by a page)", pages)
		if pdf != "" {
			rep.Linef("laid-out copy for a person to open: %s", pdf)
		}
	} else {
		// A page of this kind of document holds some 550 Chinese characters
		// or 2,800 Latin ones; a table row or a picture takes its share.
		wide := 0
		for _, p := range paras {
			for _, c := range paraText(p, viewAccepted) {
				if c > 0x2E7F {
					wide++
				}
			}
		}
		est := float64(wide)/550 + float64(chars-wide)/2800 + float64(pictures)*0.35 + float64(len(headings))*0.04
		for _, tbl := range tables {
			rowsN := 0
			for _, tr := range tbl.ChildElements() {
				if isW(tr, "tr") {
					rowsN++
				}
			}
			est += float64(rowsN) * 0.03
		}
		pages = int(est + 0.999)
		if hasTOC {
			pages += 2 // the cover and the contents
		}
		if pages < 1 {
			pages = 1
		}
		rep.Linef("pages: about %d — an ESTIMATE from the amount of text, because nothing on this machine lays a document out; the real count is Word's", pages)
	}
	if opt.Pages > 0 {
		slack := opt.Pages / 4
		if slack < 1 {
			slack = 1
		}
		if !real {
			slack += opt.Pages / 5 // an estimate is given more room before it is called wrong
		}
		if pages < opt.Pages-slack || pages > opt.Pages+slack {
			est := ""
			if !real {
				est = "an estimated "
			}
			rep.Problem("about %d pages were asked for and the document runs to %s%d", opt.Pages, est, pages)
		}
	}
	if pages > 3 && !pageField {
		rep.Problem("the document runs to %d pages and its footer has no page number", pages)
	}
	rep.Linef("not checked: how the pages look (line breaks, where a table splits, fonts as drawn); that needs Word or a person")

	if opt.Against != "" {
		compareDocuments(rep, d, opt)
	}
}

// compareDocuments sets a document against the one it was made from.
func compareDocuments(rep *Report, edited *docxFile, opt DocCheckOptions) {
	rep.Linef("")
	rep.Linef("--- compared with %s ---", opt.Against)
	orig, err := openDocx(opt.Against)
	if err != nil {
		rep.Problem("the original could not be opened: %v", err)
		return
	}
	before := orig.viewLines(viewAccepted)
	after := edited.viewLines(viewAccepted)
	if changes := edited.trackedChanges(); len(changes) > 0 {
		// What makes a tracked edit safe: reject everything and the original
		// is back, word for word.
		rejected := edited.viewLines(viewRejected)
		if equalLines(rejected, before) {
			rep.Linef("with every tracked change rejected, the text is the original, word for word (%d paragraph(s))", len(before))
		} else {
			first := "?"
			for i := 0; i < len(rejected) || i < len(before); i++ {
				a, b := "", ""
				if i < len(before) {
					a = before[i]
				}
				if i < len(rejected) {
					b = rejected[i]
				}
				if a != b {
					first = fmt.Sprintf(`"%s" -> "%s"`, clip(a, 60), clip(b, 60))
					break
				}
			}
			rep.Problem("with every tracked change rejected the text is NOT the original: something was changed without being tracked. First difference: %s", first)
		}
	} else {
		rep.Linef("no tracked changes in the edited document: the edits were made directly")
	}

	expected := func(old string) string {
		for _, e := range opt.Expect {
			old = strings.ReplaceAll(old, e[0], e[1])
		}
		return old
	}
	allowed := func(text string) bool {
		for _, a := range opt.Allowed {
			if a != "" && (strings.Contains(text, a) || strings.Contains(a, text)) {
				return true
			}
		}
		return false
	}
	strict := len(opt.Expect) > 0 || len(opt.Allowed) > 0
	changed := 0
	for _, op := range diffLines(before, after) {
		switch {
		case op.old != "" && op.new != "":
			changed++
			if strict && expected(op.old) == op.new {
				rep.Linef("changed as asked:\n    - %s\n    + %s", clip(op.old, 200), clip(op.new, 200))
			} else if strict {
				rep.Linef("UNEXPECTED change:\n    - %s\n    + %s", clip(op.old, 200), clip(op.new, 200))
				rep.Problem(`a paragraph changed in a way that was not asked for: "%s" -> "%s"`, clip(op.old, 60), clip(op.new, 60))
			} else {
				rep.Linef("changed:\n    - %s\n    + %s", clip(op.old, 200), clip(op.new, 200))
			}
		case op.old != "":
			changed++
			if strict && !allowed(op.old) {
				rep.Problem(`paragraph REMOVED, not one of the changes asked for: "%s"`, clip(op.old, 100))
			} else {
				rep.Linef(`paragraph removed%s: "%s"`, map[bool]string{true: " as asked", false: ""}[strict], clip(op.old, 100))
			}
		default:
			changed++
			if strict && !allowed(op.new) {
				rep.Problem(`paragraph ADDED, not one of the changes asked for: "%s"`, clip(op.new, 100))
			} else {
				rep.Linef(`paragraph added%s: "%s"`, map[bool]string{true: " as asked", false: ""}[strict], clip(op.new, 100))
			}
		}
	}
	joined := strings.Join(after, "\n")
	for _, e := range opt.Expect {
		if e[1] != "" && !strings.Contains(joined, e[1]) {
			rep.Problem(`"%s" was to become "%s", but "%s" is nowhere in the edited document`, e[0], e[1], e[1])
		}
		if e[0] != "" && !strings.Contains(e[1], e[0]) && strings.Contains(joined, e[0]) {
			rep.Problem(`"%s" is still in the edited document`, e[0])
		}
	}
	if changed == 0 {
		rep.Linef("no paragraph differs between the two documents")
	}

	// Paragraphs whose words did not change must not have changed their look.
	// The same words can stand in a document twice — a heading, and its line
	// in the contents — each with its own look.
	looks := map[string]map[string]bool{}
	for _, p := range orig.paragraphs() {
		if t := strings.TrimSpace(paraText(p, viewAccepted)); t != "" {
			if looks[t] == nil {
				looks[t] = map[string]bool{}
			}
			looks[t][orig.paraLook(p)] = true
		}
	}
	moved := 0
	for _, p := range edited.paragraphs() {
		t := strings.TrimSpace(paraText(p, viewAccepted))
		was, ok := looks[t]
		if t == "" || !ok || hasTracked(p) {
			continue
		}
		if !was[edited.paraLook(p)] {
			moved++
			if moved <= 5 {
				rep.Problem(`"%s" kept its words but changed its formatting`, clip(t, 50))
			}
		}
	}
	count := func(d *docxFile, tag string) int {
		n := 0
		walk(d.body, func(e *etree.Element) {
			if isW(e, tag) {
				n++
			}
		})
		return n
	}
	if a, b := count(orig, "tbl"), count(edited, "tbl"); a != b {
		rep.Problem("the number of tables changed: %d -> %d", a, b)
	}
	if a, b := count(orig, "drawing"), count(edited, "drawing"); a != b {
		rep.Problem("the number of pictures changed: %d -> %d", a, b)
	}
	// Everything that is not the text part travelled untouched.
	for _, p := range orig.parts {
		if p.name == "word/document.xml" {
			continue
		}
		if now := edited.part(p.name); now == nil {
			rep.Problem("the part %s of the original is missing from the edited document", p.name)
		} else if string(now) != string(p.data) && (strings.HasPrefix(p.name, "word/") && !strings.HasPrefix(p.name, "word/_rels/")) {
			rep.Problem("%s (styles, headers, numbering and the like live in such parts) is not what it was in the original", p.name)
		}
	}
	if moved == 0 && changed > 0 {
		rep.Linef("paragraphs whose words did not change kept their style and formatting; styles, headers and footers are byte for byte the original's")
	}
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// lineOp is one difference between two lists of lines: a line changed (both
// set), removed (old only) or added (new only).
type lineOp struct{ old, new string }

// diffLines lists the differences between two lists of lines, pairing a
// removed line with the added line most like it as one change.
func diffLines(a, b []string) []lineOp {
	// Longest common subsequence, by table: documents are hundreds of
	// paragraphs, not millions.
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var ops []lineOp
	var olds, news []string
	flush := func() {
		used := make([]bool, len(news))
		for _, o := range olds {
			best, bestScore := -1, 0.5
			for k, nw := range news {
				if used[k] {
					continue
				}
				if s := similarity(o, nw); s > bestScore {
					best, bestScore = k, s
				}
			}
			if best >= 0 {
				used[best] = true
				ops = append(ops, lineOp{o, news[best]})
			} else {
				ops = append(ops, lineOp{o, ""})
			}
		}
		for k, nw := range news {
			if !used[k] {
				ops = append(ops, lineOp{"", nw})
			}
		}
		olds, news = nil, nil
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			flush()
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			olds = append(olds, a[i])
			i++
		default:
			news = append(news, b[j])
			j++
		}
	}
	olds = append(olds, a[i:]...)
	news = append(news, b[j:]...)
	flush()
	return ops
}

// similarity is how alike two lines are, 0..1: the share of their length
// that their common prefix and suffix cover. Enough to tell an edited
// paragraph from an unrelated one.
func similarity(a, b string) float64 {
	x, y := []rune(a), []rune(b)
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	p := 0
	for p < len(x) && p < len(y) && x[p] == y[p] {
		p++
	}
	s := 0
	for s < len(x)-p && s < len(y)-p && x[len(x)-1-s] == y[len(y)-1-s] {
		s++
	}
	longer := len(x)
	if len(y) > longer {
		longer = len(y)
	}
	return float64(p+s) / float64(longer)
}
