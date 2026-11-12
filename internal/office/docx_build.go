package office

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"image"
	_ "image/gif"  // picture sizes are read from the file
	_ "image/jpeg" // …in whichever of the usual formats it is
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DocSpec describes a Word document to build. See the docx skill for the
// format as the model is told it; the JSON tags are that format.
type DocSpec struct {
	Title          string     `json:"title"`
	Subtitle       string     `json:"subtitle"`
	Author         string     `json:"author"`
	Date           string     `json:"date"`
	Cover          *bool      `json:"cover"`
	TOC            *bool      `json:"toc"`
	NumberHeadings *bool      `json:"number_headings"`
	Header         *string    `json:"header"`
	Footer         *string    `json:"footer"`
	Fonts          DocFonts   `json:"fonts"`
	Size           float64    `json:"size"`
	Content        []DocBlock `json:"content"`
}

// DocFonts names the faces: one for Latin text, one each for Chinese body
// text and headings.
type DocFonts struct {
	Body    string `json:"body"`
	Heading string `json:"heading"`
	Latin   string `json:"latin"`
}

// DocBlock is one item of content; exactly one of its fields is set.
type DocBlock struct {
	H         int       `json:"h"`
	Text      string    `json:"text"`
	P         *string   `json:"p"`
	Bullets   []string  `json:"bullets"`
	Numbered  []string  `json:"numbered"`
	Table     *DocTable `json:"table"`
	Image     *DocImage `json:"image"`
	Note      *string   `json:"note"`
	PageBreak bool      `json:"page_break"`
}

// DocTable is a table with a header row.
type DocTable struct {
	Caption string    `json:"caption"`
	Columns []string  `json:"columns"`
	Rows    [][]any   `json:"rows"`
	Widths  []float64 `json:"widths"`
}

// DocImage is a picture from a file, with its caption.
type DocImage struct {
	Path    string  `json:"path"`
	WidthCM float64 `json:"width_cm"`
	Caption string  `json:"caption"`
}

const (
	nsW   = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	nsR   = `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	nsWP  = `xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"`
	nsA   = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`
	nsPic = `xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"`

	// A4 with these margins leaves a text column this wide, in twentieths of a point.
	pageW, pageH   = 11906, 16838
	marginSide     = 1588 // 2.8 cm
	marginTop      = 1440 // 2.54 cm
	textWidthTwips = pageW - 2*marginSide
)

var (
	alreadyNumbered = regexp.MustCompile(`^(第.{1,3}[章节部分]|\d+(\.\d+)*[.、 ]|[一二三四五六七八九十]+[、.])`)
	boldSplit       = regexp.MustCompile(`\*\*`)
)

func esc(s string) string { return html.EscapeString(s) }

// docBuilder accumulates a document's body and what the body refers to.
type docBuilder struct {
	spec       DocSpec
	zh         bool
	latin      string
	bodyFont   string
	headFont   string
	size       float64 // points
	body       strings.Builder
	errs       []string
	media      []docMedia
	headings   []docHeading
	tables     int
	figures    int
	bookmarkID int
}

type docMedia struct {
	name string
	data []byte
	rid  string
}

type docHeading struct {
	level int
	text  string
	mark  string
}

func (b *docBuilder) T(zh, en string) string {
	if b.zh {
		return zh
	}
	return en
}

func (b *docBuilder) bad(format string, args ...any) {
	b.errs = append(b.errs, fmt.Sprintf(format, args...))
}

// rpr is a run's formatting: size in points (0 = the style's), bold, colour,
// and an East Asian face other than the style's.
func (b *docBuilder) rpr(size float64, bold bool, color, east string) string {
	var s strings.Builder
	if east != "" {
		fmt.Fprintf(&s, `<w:rFonts w:ascii="%s" w:hAnsi="%s" w:eastAsia="%s" w:cs="%s"/>`, esc(b.latin), esc(b.latin), esc(east), esc(b.latin))
	}
	if bold {
		s.WriteString(`<w:b/><w:bCs/>`)
	}
	if color != "" {
		fmt.Fprintf(&s, `<w:color w:val="%s"/>`, color)
	}
	if size > 0 {
		fmt.Fprintf(&s, `<w:sz w:val="%d"/><w:szCs w:val="%d"/>`, int(size*2), int(size*2))
	}
	if s.Len() == 0 {
		return ""
	}
	return "<w:rPr>" + s.String() + "</w:rPr>"
}

// runs writes text as runs, **bold** spans included.
func (b *docBuilder) runs(text string, size float64, bold bool, color, east string) string {
	var s strings.Builder
	for i, part := range boldSplit.Split(text, -1) {
		if part == "" {
			continue
		}
		s.WriteString("<w:r>" + b.rpr(size, bold || i%2 == 1, color, east) + `<w:t xml:space="preserve">` + esc(part) + "</w:t></w:r>")
	}
	return s.String()
}

func (b *docBuilder) para(ppr, content string) {
	b.body.WriteString("<w:p>")
	if ppr != "" {
		b.body.WriteString("<w:pPr>" + ppr + "</w:pPr>")
	}
	b.body.WriteString(content + "</w:p>")
}

func (b *docBuilder) pageBreak() {
	b.body.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
}

func (b *docBuilder) cover() {
	for i := 0; i < 6; i++ {
		b.para("", "")
	}
	b.para(`<w:spacing w:line="312" w:lineRule="auto"/><w:jc w:val="center"/>`, b.runs(b.spec.Title, 26, true, "", b.headFont))
	if b.spec.Subtitle != "" {
		b.para(`<w:jc w:val="center"/>`, b.runs(b.spec.Subtitle, 16, false, "444444", b.headFont))
	}
	for i := 0; i < 9; i++ {
		b.para("", "")
	}
	for _, v := range []string{b.spec.Author, b.spec.Date} {
		if v != "" {
			b.para(`<w:jc w:val="center"/>`, b.runs(v, 14, false, "", ""))
		}
	}
	b.pageBreak()
}

// toc writes a real contents field with its entries already in it.
//
// Word rebuilds everything between the field's begin and end when the field
// is updated, page numbers included, and the document asks for that on open.
// Until then — and in every program that does not update fields: a preview, a
// phone — what shows is what is written here: each heading, linked to its
// place. The page numbers are left for Word: working them out needs the
// document laid out, which is the one thing this cannot do.
func (b *docBuilder) toc(planned []docHeading) {
	b.para(`<w:spacing w:after="240"/><w:jc w:val="center"/>`, b.runs(b.T("目  录", "Contents"), 16, true, "", b.headFont))
	for i, h := range planned {
		var s strings.Builder
		if i == 0 {
			s.WriteString(`<w:r><w:fldChar w:fldCharType="begin"/></w:r>` +
				`<w:r><w:instrText xml:space="preserve"> TOC \o "1-3" \h \z \u </w:instrText></w:r>` +
				`<w:r><w:fldChar w:fldCharType="separate"/></w:r>`)
		}
		fmt.Fprintf(&s, `<w:hyperlink w:anchor="%s" w:history="1">%s</w:hyperlink>`, h.mark, b.runs(h.text, 0, h.level == 1, "", ""))
		if i == len(planned)-1 {
			s.WriteString(`<w:r><w:fldChar w:fldCharType="end"/></w:r>`)
		}
		b.para(fmt.Sprintf(`<w:pStyle w:val="TOC%d"/>`, h.level), s.String())
	}
	b.pageBreak()
}

func (b *docBuilder) heading(h docHeading) {
	b.bookmarkID++
	b.para(fmt.Sprintf(`<w:pStyle w:val="Heading%d"/>`, h.level),
		fmt.Sprintf(`<w:bookmarkStart w:id="%d" w:name="%s"/>`, b.bookmarkID, h.mark)+
			b.runs(h.text, 0, false, "", "")+
			fmt.Sprintf(`<w:bookmarkEnd w:id="%d"/>`, b.bookmarkID))
	b.headings = append(b.headings, h)
}

func (b *docBuilder) table(t *DocTable, where string) {
	if len(t.Columns) == 0 || len(t.Rows) == 0 {
		b.bad(`%s: a table needs "columns" and "rows"`, where)
		return
	}
	rows := make([][]string, 0, len(t.Rows)+1)
	rows = append(rows, t.Columns)
	for _, r := range t.Rows {
		if len(r) != len(t.Columns) {
			b.bad("%s: every table row needs %d cells, one per column", where, len(t.Columns))
			return
		}
		cells := make([]string, len(r))
		for i, v := range r {
			switch x := v.(type) {
			case nil:
			case string:
				cells[i] = x
			case float64:
				cells[i] = formatNumber(fmt.Sprintf("%v", x))
			default:
				cells[i] = fmt.Sprintf("%v", x)
			}
		}
		rows = append(rows, cells)
	}
	b.tables++
	if t.Caption != "" {
		b.para(`<w:keepNext/><w:spacing w:after="80"/><w:jc w:val="center"/>`,
			b.runs(fmt.Sprintf("%s %d  %s", b.T("表", "Table"), b.tables, t.Caption), 10.5, true, "", b.headFont))
	}
	weights := t.Widths
	if len(weights) != len(t.Columns) {
		weights = make([]float64, len(t.Columns))
		for c := range t.Columns {
			longest := 6
			for _, r := range rows {
				if w := displayWidth(r[c]); w > longest {
					longest = w
				}
			}
			if longest > 40 {
				longest = 40
			}
			weights[c] = float64(longest)
		}
	}
	total := 0.0
	for _, w := range weights {
		total += w
	}
	widths := make([]int, len(weights))
	for i, w := range weights {
		widths[i] = int(float64(textWidthTwips) * w / total)
	}
	s := &b.body
	s.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/>` +
		fmt.Sprintf(`<w:tblW w:w="%d" w:type="dxa"/>`, textWidthTwips) +
		`<w:jc w:val="center"/><w:tblLayout w:type="fixed"/></w:tblPr><w:tblGrid>`)
	for _, w := range widths {
		fmt.Fprintf(s, `<w:gridCol w:w="%d"/>`, w)
	}
	s.WriteString(`</w:tblGrid>`)
	for ri, r := range rows {
		s.WriteString(`<w:tr>`)
		if ri == 0 {
			// The header row comes again at the top of each page the table runs onto.
			s.WriteString(`<w:trPr><w:tblHeader/></w:trPr>`)
		}
		for ci, v := range r {
			fmt.Fprintf(s, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/>`, widths[ci])
			ppr := `<w:spacing w:after="40" w:line="288" w:lineRule="auto"/>`
			if ri == 0 {
				s.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="E7ECF3"/>`)
				ppr += `<w:jc w:val="center"/>`
			}
			s.WriteString(`</w:tcPr><w:p><w:pPr>` + ppr + `</w:pPr>` + b.runs(v, 10.5, ri == 0, "", "") + `</w:p></w:tc>`)
		}
		s.WriteString(`</w:tr>`)
	}
	s.WriteString(`</w:tbl>`)
	b.para(`<w:spacing w:after="40"/>`, "")
}

func (b *docBuilder) image(im *DocImage, where, base string) {
	path := im.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.bad(`%s: the image "%s" does not exist`, where, im.Path)
		return
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 {
		b.bad(`%s: "%s" is not a PNG, JPEG or GIF picture`, where, im.Path)
		return
	}
	width := im.WidthCM
	if width <= 0 {
		width = 14
	}
	if width > 15.4 {
		width = 15.4
	}
	cx := int64(width * 360000)
	cy := cx * int64(cfg.Height) / int64(cfg.Width)
	n := len(b.media) + 1
	ext := map[string]string{"jpeg": "jpg"}[format]
	if ext == "" {
		ext = format
	}
	m := docMedia{name: fmt.Sprintf("image%d.%s", n, ext), data: data, rid: fmt.Sprintf("rIdImg%d", n)}
	b.media = append(b.media, m)
	b.figures++
	drawing := fmt.Sprintf(`<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%d" cy="%d"/>`+
		`<wp:docPr id="%d" name="Picture %d"/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">`+
		`<pic:pic><pic:nvPicPr><pic:cNvPr id="%d" name="%s"/><pic:cNvPicPr/></pic:nvPicPr>`+
		`<pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>`+
		`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>`+
		`</pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`, cx, cy, n, n, n, esc(m.name), m.rid, cx, cy)
	b.para(`<w:keepNext/><w:jc w:val="center"/>`, drawing)
	if im.Caption != "" {
		b.para(`<w:jc w:val="center"/>`, b.runs(fmt.Sprintf("%s %d  %s", b.T("图", "Figure"), b.figures, im.Caption), 10.5, true, "", b.headFont))
	}
}

// planHeadings numbers the headings and catches what is wrong with the outline.
func (b *docBuilder) planHeadings() []docHeading {
	var planned []docHeading
	counters := [3]int{}
	prev := 0
	number := b.spec.NumberHeadings == nil || *b.spec.NumberHeadings
	content := b.spec.Content
	for i, item := range content {
		if item.H == 0 {
			continue
		}
		where := fmt.Sprintf("item %d", i+1)
		text := strings.TrimSpace(item.Text)
		if item.H < 1 || item.H > 3 {
			b.bad("%s: heading level must be 1, 2 or 3", where)
			continue
		}
		if text == "" {
			b.bad("%s: a heading with no text", where)
			continue
		}
		if item.H > prev+1 {
			b.bad(`%s: "%s" is level %d right after level %d; a level cannot be skipped`, where, text, item.H, prev)
		}
		prev = item.H
		counters[item.H-1]++
		for k := item.H; k < 3; k++ {
			counters[k] = 0
		}
		if number && !alreadyNumbered.MatchString(text) {
			parts := make([]string, item.H)
			for k := 0; k < item.H; k++ {
				parts[k] = fmt.Sprint(counters[k])
			}
			text = strings.Join(parts, ".") + " " + text
		}
		if i+1 >= len(content) || (content[i+1].H != 0 && content[i+1].H <= item.H) {
			b.bad(`%s: the heading "%s" has nothing under it`, where, text)
		}
		planned = append(planned, docHeading{item.H, text, fmt.Sprintf("_h%d", len(planned)+1)})
	}
	return planned
}

// BuildDocument writes a Word document from its description.
//
// Headings are real heading styles, the contents are a real TOC field with
// its entries filled in, tables repeat their header row over a page break,
// figures and tables are captioned and numbered, pages are numbered in the
// footer, and all text names an East Asian font. base is the folder relative
// image paths are taken from.
//
// It refuses, by item, what would not be a sound document; nothing is written
// until that list is empty.
func BuildDocument(specJSON []byte, out, base string) *Report {
	var spec DocSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return NotWritten([]string{"the spec is not valid JSON: " + err.Error()})
	}
	b := &docBuilder{spec: spec}
	b.zh = hasCJK(string(specJSON))
	b.latin = spec.Fonts.Latin
	if b.latin == "" {
		b.latin = "Times New Roman"
	}
	b.bodyFont, b.headFont = spec.Fonts.Body, spec.Fonts.Heading
	if b.bodyFont == "" {
		b.bodyFont = b.T("宋体", b.latin)
	}
	if b.headFont == "" {
		b.headFont = b.T("黑体", "Arial")
	}
	b.size = spec.Size
	if b.size <= 0 {
		b.size = 12
	}
	if len(spec.Content) == 0 {
		return NotWritten([]string{`the spec has no "content"`})
	}
	if strings.TrimSpace(spec.Title) == "" {
		b.bad(`the document has no "title"`)
	}
	cover := spec.Cover == nil || *spec.Cover
	planned := b.planHeadings()
	if cover {
		b.cover()
	}
	if (spec.TOC == nil || *spec.TOC) && len(planned) > 0 {
		b.toc(planned)
	}
	indent := ""
	if b.zh {
		indent = fmt.Sprintf(`<w:ind w:firstLine="%d"/>`, int(b.size*2*20))
	}
	next := 0
	for i, item := range spec.Content {
		where := fmt.Sprintf("item %d", i+1)
		switch {
		case item.H != 0:
			if item.H >= 1 && item.H <= 3 && strings.TrimSpace(item.Text) != "" && next < len(planned) {
				b.heading(planned[next])
				next++
			}
		case item.P != nil:
			b.para(indent+`<w:jc w:val="both"/>`, b.runs(*item.P, 0, false, "", ""))
		case item.Bullets != nil:
			for _, t := range item.Bullets {
				b.para(`<w:pStyle w:val="ListBullet"/>`, b.runs(t, 0, false, "", ""))
			}
		case item.Numbered != nil:
			// Numbers are written out: a numbered-list style carries on
			// counting from the list before it.
			for k, t := range item.Numbered {
				b.para(`<w:ind w:left="420" w:hanging="340"/>`, b.runs(fmt.Sprintf("%d. %s", k+1, t), 0, false, "", ""))
			}
		case item.Table != nil:
			b.table(item.Table, where)
		case item.Image != nil:
			b.image(item.Image, where, base)
		case item.Note != nil:
			b.para(`<w:ind w:left="420"/>`, b.runs(*item.Note, b.size-1.5, false, "555555", ""))
		case item.PageBreak:
			b.pageBreak()
		default:
			b.bad("%s: an item with nothing in it; use h, p, bullets, numbered, table, image, note or page_break", where)
		}
	}
	if len(b.errs) > 0 {
		return NotWritten(b.errs)
	}

	header := spec.Title
	if spec.Header != nil {
		header = *spec.Header
	}
	footer := "page"
	if spec.Footer != nil {
		footer = *spec.Footer
	}
	files := b.parts(cover, header, footer)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return NotWritten([]string{err.Error()})
	}
	if err := writeZip(out, files); err != nil {
		return NotWritten([]string{"the document could not be written: " + err.Error()})
	}
	rep := &Report{}
	rep.Linef("wrote %s: %d heading(s), %d table(s), %d figure(s)", out, len(b.headings), b.tables, b.figures)
	rep.Linef("")
	rep.Linef("--- check ---")
	CheckDocumentInto(rep, out, DocCheckOptions{})
	return rep
}

type zipPart struct {
	name string
	data []byte
}

func writeZip(path string, files []zipPart) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return err
		}
		if _, err := w.Write(f.data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

const xmlHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// parts are the files a .docx is made of.
func (b *docBuilder) parts(cover bool, header, footer string) []zipPart {
	hasHeader := header != "" && header != "none"
	hasFooter := footer != "" && footer != "none"

	var sect strings.Builder
	sect.WriteString(`<w:sectPr>`)
	if hasHeader {
		sect.WriteString(`<w:headerReference w:type="default" r:id="rIdHeader"/>`)
	}
	if hasFooter {
		sect.WriteString(`<w:footerReference w:type="default" r:id="rIdFooter"/>`)
	}
	fmt.Fprintf(&sect, `<w:pgSz w:w="%d" w:h="%d"/><w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="851" w:footer="992" w:gutter="0"/>`,
		pageW, pageH, marginTop, marginSide, marginTop, marginSide)
	if cover {
		// The cover carries neither header nor page number.
		sect.WriteString(`<w:titlePg/>`)
	}
	sect.WriteString(`</w:sectPr>`)

	document := xmlHead + `<w:document ` + nsW + ` ` + nsR + ` ` + nsWP + ` ` + nsA + ` ` + nsPic + `><w:body>` +
		b.body.String() + sect.String() + `</w:body></w:document>`

	rels := xmlHead + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rIdStyles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`<Relationship Id="rIdSettings" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/>` +
		`<Relationship Id="rIdNumbering" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>`
	types := xmlHead + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Default Extension="png" ContentType="image/png"/><Default Extension="jpg" ContentType="image/jpeg"/><Default Extension="gif" ContentType="image/gif"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
		`<Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/>` +
		`<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>` +
		`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`

	files := []zipPart{}
	if hasHeader {
		rels += `<Relationship Id="rIdHeader" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>`
		types += `<Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/>`
		files = append(files, zipPart{"word/header1.xml", []byte(xmlHead + `<w:hdr ` + nsW + `><w:p><w:pPr><w:pStyle w:val="Header"/><w:jc w:val="right"/></w:pPr>` +
			b.runs(header, 9, false, "666666", "") + `</w:p></w:hdr>`)})
	}
	if hasFooter {
		rels += `<Relationship Id="rIdFooter" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>`
		types += `<Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/>`
		field := func(instr string) string {
			return `<w:fldSimple w:instr=" ` + instr + ` "><w:r>` + b.rpr(9, false, "", "") + `<w:t>1</w:t></w:r></w:fldSimple>`
		}
		line := b.runs(footer, 9, false, "666666", "")
		if footer == "page" {
			if b.zh {
				line = b.runs("第 ", 9, false, "", "") + field("PAGE") + b.runs(" 页 / 共 ", 9, false, "", "") + field("NUMPAGES") + b.runs(" 页", 9, false, "", "")
			} else {
				line = b.runs("Page ", 9, false, "", "") + field("PAGE") + b.runs(" of ", 9, false, "", "") + field("NUMPAGES")
			}
		}
		files = append(files, zipPart{"word/footer1.xml", []byte(xmlHead + `<w:ftr ` + nsW + `><w:p><w:pPr><w:pStyle w:val="Footer"/><w:jc w:val="center"/></w:pPr>` + line + `</w:p></w:ftr>`)})
	}
	for _, m := range b.media {
		rels += fmt.Sprintf(`<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/%s"/>`, m.rid, m.name)
		files = append(files, zipPart{"word/media/" + m.name, m.data})
	}
	rels += `</Relationships>`
	types += `</Types>`

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	core := xmlHead + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + esc(b.spec.Title) + `</dc:title><dc:creator>` + esc(b.spec.Author) + `</dc:creator>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + now + `</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + now + `</dcterms:modified></cp:coreProperties>`

	// Fields — the contents, the page counts — are refreshed when Word opens the file.
	settings := xmlHead + `<w:settings ` + nsW + `><w:updateFields w:val="true"/><w:defaultTabStop w:val="420"/>` +
		`<w:characterSpacingControl w:val="compressPunctuation"/></w:settings>`
	numbering := xmlHead + `<w:numbering ` + nsW + `><w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="singleLevel"/>` +
		`<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="•"/><w:lvlJc w:val="left"/>` +
		`<w:pPr><w:ind w:left="420" w:hanging="300"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:hint="default"/></w:rPr></w:lvl>` +
		`</w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num></w:numbering>`

	return append([]zipPart{
		{"[Content_Types].xml", []byte(types)},
		{"_rels/.rels", []byte(xmlHead + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/></Relationships>`)},
		{"docProps/core.xml", []byte(core)},
		{"word/document.xml", []byte(document)},
		{"word/_rels/document.xml.rels", []byte(rels)},
		{"word/styles.xml", []byte(b.styles())},
		{"word/settings.xml", []byte(settings)},
		{"word/numbering.xml", []byte(numbering)},
	}, files...)
}

// styles is the document's style sheet. The defaults name both a Latin and an
// East Asian face, so text in any style has a Chinese font; headings carry
// outline levels, which is what makes them headings to Word.
func (b *docBuilder) styles() string {
	fonts := func(east string) string {
		return fmt.Sprintf(`<w:rFonts w:ascii="%s" w:hAnsi="%s" w:eastAsia="%s" w:cs="%s"/>`, esc(b.latin), esc(b.latin), esc(east), esc(b.latin))
	}
	half := int(b.size * 2)
	var s strings.Builder
	s.WriteString(xmlHead + `<w:styles ` + nsW + `><w:docDefaults><w:rPrDefault><w:rPr>` + fonts(b.bodyFont) +
		fmt.Sprintf(`<w:sz w:val="%d"/><w:szCs w:val="%d"/><w:lang w:val="en-US" w:eastAsia="zh-CN" w:bidi="ar-SA"/>`, half, half) +
		`</w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="360" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>`)
	s.WriteString(`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/><w:pPr><w:widowControl w:val="0"/></w:pPr><w:rPr>` + fonts(b.bodyFont) + `</w:rPr></w:style>`)
	sizes := [3]int{32, 28, 24}
	before := [3]int{360, 240, 240}
	for lvl := 1; lvl <= 3; lvl++ {
		fmt.Fprintf(&s, `<w:style w:type="paragraph" w:styleId="Heading%d"><w:name w:val="heading %d"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/>`+
			`<w:pPr><w:keepNext/><w:keepLines/><w:spacing w:before="%d" w:after="160" w:line="312" w:lineRule="auto"/><w:outlineLvl w:val="%d"/></w:pPr>`+
			`<w:rPr>%s<w:b/><w:bCs/><w:sz w:val="%d"/><w:szCs w:val="%d"/></w:rPr></w:style>`, lvl, lvl, before[lvl-1], lvl-1, fonts(b.headFont), sizes[lvl-1], sizes[lvl-1])
		fmt.Fprintf(&s, `<w:style w:type="paragraph" w:styleId="TOC%d"><w:name w:val="toc %d"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/>`+
			`<w:pPr><w:tabs><w:tab w:val="right" w:leader="dot" w:pos="%d"/></w:tabs><w:spacing w:after="40" w:line="312" w:lineRule="auto"/><w:ind w:left="%d"/></w:pPr></w:style>`,
			lvl, lvl, textWidthTwips, (lvl-1)*420)
	}
	s.WriteString(`<w:style w:type="paragraph" w:styleId="ListBullet"><w:name w:val="List Bullet"/><w:basedOn w:val="Normal"/><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr><w:spacing w:after="60"/></w:pPr></w:style>`)
	s.WriteString(`<w:style w:type="paragraph" w:styleId="Header"><w:name w:val="header"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/></w:pPr></w:style>`)
	s.WriteString(`<w:style w:type="paragraph" w:styleId="Footer"><w:name w:val="footer"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/></w:pPr></w:style>`)
	s.WriteString(`<w:style w:type="character" w:styleId="Hyperlink"><w:name w:val="Hyperlink"/></w:style>`)
	s.WriteString(`<w:style w:type="table" w:default="1" w:styleId="TableNormal"><w:name w:val="Normal Table"/><w:tblPr><w:tblInd w:w="0" w:type="dxa"/>` +
		`<w:tblCellMar><w:top w:w="0" w:type="dxa"/><w:left w:w="108" w:type="dxa"/><w:bottom w:w="0" w:type="dxa"/><w:right w:w="108" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style>`)
	border := func(side string) string {
		return fmt.Sprintf(`<w:%s w:val="single" w:sz="4" w:space="0" w:color="8C96A3"/>`, side)
	}
	s.WriteString(`<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:basedOn w:val="TableNormal"/><w:tblPr><w:tblBorders>` +
		border("top") + border("left") + border("bottom") + border("right") + border("insideH") + border("insideV") +
		`</w:tblBorders></w:tblPr></w:style>`)
	s.WriteString(`</w:styles>`)
	return s.String()
}
