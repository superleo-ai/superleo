package office

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/beevik/etree"
)

// A Word document, opened for reading or for a careful change.
//
// A .docx is a zip of XML parts. Only the one that holds the text —
// word/document.xml — is ever parsed and written back here; every other part
// is carried over byte for byte, which is most of what "leave everything else
// as it was" means. The text part is handled as a tree that keeps the
// document's own prefixes and attribute order, so what was not touched is
// written back as it was read.

type docxFile struct {
	parts []zipPart // every part, in the file's order
	doc   *etree.Document
	body  *etree.Element
	// styles maps a style id to its element in word/styles.xml.
	styles      map[string]*etree.Element
	defaultPara string
	docDefaults *etree.Element
}

func openDocx(path string) (*docxFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("not a .docx (it is not a zip archive): %w", err)
	}
	d := &docxFile{styles: map[string]*etree.Element{}}
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		d.parts = append(d.parts, zipPart{zf.Name, data})
	}
	main := d.part("word/document.xml")
	if main == nil {
		return nil, fmt.Errorf("not a Word document: it has no word/document.xml")
	}
	d.doc = etree.NewDocument()
	if err := d.doc.ReadFromBytes(main); err != nil {
		return nil, fmt.Errorf("the document's text part is not well-formed XML: %w", err)
	}
	root := d.doc.Root()
	if root == nil || root.Space != "w" {
		return nil, fmt.Errorf("the document does not use the usual \"w:\" prefix for its text; it cannot be handled here")
	}
	d.body = child(root, "body")
	if d.body == nil {
		return nil, fmt.Errorf("the document has no body")
	}
	if st := d.part("word/styles.xml"); st != nil {
		sdoc := etree.NewDocument()
		if sdoc.ReadFromBytes(st) == nil && sdoc.Root() != nil {
			for _, s := range sdoc.Root().ChildElements() {
				switch s.Tag {
				case "style":
					id := attr(s, "styleId")
					d.styles[id] = s
					if attr(s, "type") == "paragraph" && attr(s, "default") == "1" {
						d.defaultPara = id
					}
				case "docDefaults":
					d.docDefaults = s
				}
			}
		}
	}
	return d, nil
}

func (d *docxFile) part(name string) []byte {
	for _, p := range d.parts {
		if p.name == name {
			return p.data
		}
	}
	return nil
}

// partsLike returns the parts whose name matches, in name order.
func (d *docxFile) partsLike(re *regexp.Regexp) []zipPart {
	var out []zipPart
	for _, p := range d.parts {
		if re.MatchString(p.name) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// save writes the document to path with its text part replaced by the tree
// as it now is, and every other part unchanged.
func (d *docxFile) save(path string) error {
	d.doc.WriteSettings = etree.WriteSettings{CanonicalText: true, CanonicalAttrVal: true}
	text, err := d.doc.WriteToBytes()
	if err != nil {
		return err
	}
	out := make([]zipPart, len(d.parts))
	for i, p := range d.parts {
		out[i] = p
		if p.name == "word/document.xml" {
			out[i].data = text
		}
	}
	return writeZip(path, out)
}

// --- the tree ---------------------------------------------------------------

func isW(e *etree.Element, tag string) bool { return e != nil && e.Space == "w" && e.Tag == tag }

func child(e *etree.Element, tag string) *etree.Element {
	if e == nil {
		return nil
	}
	for _, c := range e.ChildElements() {
		if isW(c, tag) {
			return c
		}
	}
	return nil
}

func attr(e *etree.Element, name string) string {
	if e == nil {
		return ""
	}
	return e.SelectAttrValue("w:"+name, "")
}

// walk visits every element under e, depth first, in document order.
func walk(e *etree.Element, visit func(*etree.Element)) {
	for _, c := range e.ChildElements() {
		visit(c)
		walk(c, visit)
	}
}

// paragraphs is every paragraph of the body, in order, tables included.
func (d *docxFile) paragraphs() []*etree.Element {
	var out []*etree.Element
	walk(d.body, func(e *etree.Element) {
		if isW(e, "p") {
			out = append(out, e)
		}
	})
	return out
}

func inTable(p *etree.Element) bool {
	for a := p.Parent(); a != nil; a = a.Parent() {
		if isW(a, "tc") {
			return true
		}
	}
	return false
}

// changeState says whether an element sits inside a tracked insertion or
// deletion within its paragraph.
func changeState(e, p *etree.Element) string {
	state := ""
	for a := e.Parent(); a != nil && a != p; a = a.Parent() {
		if isW(a, "ins") {
			state = "ins"
		} else if isW(a, "del") {
			state = "del"
		}
	}
	return state
}

// Views of a paragraph's text. With tracked changes in a document there are
// two texts in it at once: as it reads with every change accepted, and as it
// read before them.
const (
	viewAccepted = "accepted"
	viewRejected = "rejected"
	viewMarked   = "marked" // both: deletions as [-…-], insertions as {+…+}
)

func paraText(p *etree.Element, view string) string {
	var b strings.Builder
	walk(p, func(e *etree.Element) {
		if e.Space != "w" {
			return
		}
		var text string
		switch e.Tag {
		case "t", "delText":
			text = e.Text()
		case "tab":
			text = "\t"
		case "br":
			text = "\n"
		default:
			return
		}
		state := changeState(e, p)
		switch {
		case state == "del" || e.Tag == "delText":
			if view == viewRejected {
				b.WriteString(text)
			} else if view == viewMarked {
				b.WriteString("[-" + text + "-]")
			}
		case state == "ins":
			if view == viewAccepted {
				b.WriteString(text)
			} else if view == viewMarked {
				b.WriteString("{+" + text + "+}")
			}
		default:
			b.WriteString(text)
		}
	})
	return b.String()
}

// markChange reports whether the paragraph itself — its mark — is a tracked
// insertion or deletion.
func markChange(p *etree.Element, kind string) bool {
	return child(child(child(p, "pPr"), "rPr"), kind) != nil
}

var runsOfSpace = regexp.MustCompile(`[ \t]+`)

// viewLines is the document as lines of text in one view. A paragraph the
// view says is not there — inserted, when rejected; deleted, when accepted —
// is left out.
func (d *docxFile) viewLines(view string) []string {
	var lines []string
	for _, p := range d.paragraphs() {
		text := strings.TrimSpace(runsOfSpace.ReplaceAllString(paraText(p, view), " "))
		if text == "" {
			continue
		}
		lines = append(lines, text)
	}
	return lines
}

func (d *docxFile) styleID(p *etree.Element) string {
	if id := attr(child(child(p, "pPr"), "pStyle"), "val"); id != "" {
		return id
	}
	return d.defaultPara
}

func (d *docxFile) styleName(p *etree.Element) string {
	id := d.styleID(p)
	if s, ok := d.styles[id]; ok {
		if n := attr(child(s, "name"), "val"); n != "" {
			return n
		}
	}
	if id == "" {
		return "Normal"
	}
	return id
}

var headingName = regexp.MustCompile(`(?i)^(?:heading|标题)\s*(\d)$`)

// headingLevel is 1..9 when the paragraph is a real heading — by its style's
// name, or by an outline level on it or on a style it is based on — else 0.
func (d *docxFile) headingLevel(p *etree.Element) int {
	if m := headingName.FindStringSubmatch(strings.TrimSpace(d.styleName(p))); m != nil {
		return int(m[1][0] - '0')
	}
	level := func(ppr *etree.Element) int {
		if v := attr(child(ppr, "outlineLvl"), "val"); v != "" && v[0] >= '0' && v[0] <= '8' {
			return int(v[0]-'0') + 1
		}
		return 0
	}
	if l := level(child(p, "pPr")); l > 0 {
		return l
	}
	seen := map[string]bool{}
	for id := d.styleID(p); id != "" && !seen[id]; {
		seen[id] = true
		s := d.styles[id]
		if s == nil {
			break
		}
		if l := level(child(s, "pPr")); l > 0 {
			return l
		}
		id = attr(child(s, "basedOn"), "val")
	}
	return 0
}

// eastFont is the East Asian font that applies to a run: its own, its
// paragraph style's (or one that is based on), or the document defaults'.
func (d *docxFile) eastFont(p, r *etree.Element) string {
	from := func(rpr *etree.Element) string {
		f := child(rpr, "rFonts")
		if f == nil {
			return ""
		}
		if v := attr(f, "eastAsia"); v != "" {
			return v
		}
		if v := attr(f, "eastAsiaTheme"); v != "" {
			return "theme:" + v
		}
		return ""
	}
	if v := from(child(r, "rPr")); v != "" {
		return v
	}
	chain := []string{attr(child(child(r, "rPr"), "rStyle"), "val"), d.styleID(p)}
	for _, start := range chain {
		seen := map[string]bool{}
		for id := start; id != "" && !seen[id]; {
			seen[id] = true
			s := d.styles[id]
			if s == nil {
				break
			}
			if v := from(child(s, "rPr")); v != "" {
				return v
			}
			id = attr(child(s, "basedOn"), "val")
		}
	}
	return from(child(child(d.docDefaults, "rPrDefault"), "rPr"))
}

// runLook is the formatting set on a run itself, as a comparable string.
func runLook(r *etree.Element) string {
	rpr := child(r, "rPr")
	if rpr == nil {
		return ""
	}
	var parts []string
	for _, c := range rpr.ChildElements() {
		switch c.Tag {
		case "ins", "del", "rPrChange", "lang", "noProof":
			continue
		}
		var attrs []string
		for _, a := range c.Attr {
			attrs = append(attrs, a.Key+"="+a.Value)
		}
		sort.Strings(attrs)
		parts = append(parts, c.Tag+"{"+strings.Join(attrs, ",")+"}")
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// paraLook is what a paragraph looks like, for telling whether one whose
// words did not change kept its formatting: its style, its alignment, and
// the set of run formattings its text is in.
func (d *docxFile) paraLook(p *etree.Element) string {
	looks := map[string]bool{}
	walk(p, func(e *etree.Element) {
		if !isW(e, "r") {
			return
		}
		for _, t := range e.ChildElements() {
			if (isW(t, "t") || isW(t, "delText")) && t.Text() != "" {
				looks[runLook(e)] = true
				return
			}
		}
	})
	keys := make([]string, 0, len(looks))
	for k := range looks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return d.styleName(p) + "|" + attr(child(child(p, "pPr"), "jc"), "val") + "|" + strings.Join(keys, ";")
}

// trackedChange is one tracked insertion or deletion.
type trackedChange struct{ kind, author, date, text string }

func (d *docxFile) trackedChanges() []trackedChange {
	var out []trackedChange
	walk(d.body, func(e *etree.Element) {
		if !isW(e, "ins") && !isW(e, "del") {
			return
		}
		kind := map[string]string{"ins": "inserted", "del": "deleted"}[e.Tag]
		var text strings.Builder
		if isW(e.Parent(), "rPr") {
			kind = "paragraph " + kind
		} else {
			walk(e, func(t *etree.Element) {
				if isW(t, "t") || isW(t, "delText") {
					text.WriteString(t.Text())
				}
			})
		}
		out = append(out, trackedChange{kind, attr(e, "author"), attr(e, "date"), text.String()})
	})
	return out
}

func hasTracked(p *etree.Element) bool {
	found := false
	walk(p, func(e *etree.Element) {
		if isW(e, "ins") || isW(e, "del") {
			found = true
		}
	})
	return found
}
