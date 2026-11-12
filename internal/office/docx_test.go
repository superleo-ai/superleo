package office

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const planSpec = `{"title":"智慧园区能耗管理平台项目方案","subtitle":"建设方案","author":"SuperLeo Studio","date":"2026-11",
"content":[
 {"h":1,"text":"项目背景"},
 {"p":"随着园区规模扩大，能耗数据分散在多个子系统中，缺少统一的采集、分析与**优化**手段。"},
 {"h":2,"text":"现状问题"},{"bullets":["数据孤岛：电、水、气表计各自为政","人工抄表误差大、时效差"]},
 {"h":2,"text":"建设目标"},{"numbered":["实现全园区能耗数据分钟级采集","综合能耗下降 8% 以上"]},
 {"h":1,"text":"总体方案"},{"p":"平台采用“端-边-云”三层架构。"},
 {"image":{"path":"arch.png","width_cm":10,"caption":"系统总体架构"}},
 {"table":{"caption":"项目里程碑","columns":["阶段","时间","交付物"],
   "rows":[["需求调研","第 1–2 周","需求规格说明书"],["系统设计","第 3–5 周","概要设计"],["开发实施","第 6–14 周","可运行系统"]]}},
 {"h":1,"text":"投资估算"},{"p":"总投资约 280 万元。"},{"note":"说明：本章数据为示例。"}]}`

func writePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := 0; x < 40; x++ {
		for y := 0; y < 20; y++ {
			img.Set(x, y, color.RGBA{30, 100, 220, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// A built document is one Word treats as a document: real heading styles, a
// contents field that lists them, a numbered footer, Chinese text with a
// Chinese font named, a table, a picture.
func TestBuildDocument(t *testing.T) {
	t.Setenv("SUPERLEO_NO_OFFICE", "1") // the same result on every machine
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "arch.png"))
	out := filepath.Join(dir, "plan.docx")
	rep := BuildDocument([]byte(planSpec), out, dir)
	text := rep.String()
	if !rep.OK() {
		t.Fatalf("build reported problems:\n%s", text)
	}
	mustContain(t, text,
		"5 heading(s), 1 table(s), 1 figure(s)",
		"header: 智慧园区能耗管理平台项目方案",
		"[page number field]",
		"table of contents: a TOC field",
		"  1 项目背景  [heading 1]", "    1.1 现状问题  [heading 2]", "  3 投资估算  [heading 1]",
		"the table of contents lists all 5 heading(s)",
		"fonts for Chinese text: 宋体", "黑体",
		"table 1: 4 row(s) x 3 column(s)", "1 picture(s)",
		"an ESTIMATE", "RESULT: OK")

	d, err := openDocx(out)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(d.part("word/document.xml"))
	mustContain(t, doc, `<w:pStyle w:val="Heading1"/>`, `TOC \o "1-3"`, `<w:tblHeader/>`, `r:embed="rIdImg1"`, `<w:titlePg/>`, `w:anchor="_h1"`)
	mustContain(t, string(d.part("word/styles.xml")), `w:eastAsia="宋体"`, `w:eastAsia="黑体"`, `<w:outlineLvl w:val="0"/>`)
	mustContain(t, string(d.part("word/settings.xml")), `<w:updateFields w:val="true"/>`)
	if d.part("word/media/image1.png") == nil {
		t.Error("the picture is not in the file")
	}
}

func TestBuildDocumentRefusesABrokenOutline(t *testing.T) {
	out := filepath.Join(t.TempDir(), "bad.docx")
	rep := BuildDocument([]byte(`{"title":"T","content":[
	  {"h":1,"text":"A"},{"h":3,"text":"skipped"},{"p":"x"},
	  {"h":1,"text":"empty"},
	  {"h":1,"text":"B"},{"table":{"columns":["a","b"],"rows":[["only one"]]}},
	  {"image":{"path":"nope.png"}}]}`), out, t.TempDir())
	if rep.OK() {
		t.Fatal("a broken outline was accepted")
	}
	mustContain(t, rep.String(), "NOT WRITTEN", "a level cannot be skipped", `the heading "2 empty" has nothing under it`,
		"every table row needs 2 cells", `the image "nope.png" does not exist`)
	if _, err := os.Stat(out); err == nil {
		t.Error("a file was written although the build refused")
	}
}

// contractDocx is a document as another program would have written it: the
// words to change are split across runs with different formatting, which is
// what real documents are like and what a naive replace gets wrong.
func contractDocx(t *testing.T, path string) {
	t.Helper()
	body := `<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>服务合同</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t xml:space="preserve">甲方：Acme 贸易有限公司 </w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>第二条 费用</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:jc w:val="both"/></w:pPr><w:r><w:t xml:space="preserve">服务费用为 </w:t></w:r>` +
		`<w:r><w:rPr><w:b/><w:color w:val="C00000"/></w:rPr><w:t>USD </w:t></w:r><w:r><w:rPr><w:b/><w:color w:val="C00000"/></w:rPr><w:t>500</w:t></w:r>` +
		`<w:r><w:t>，交付后 30 日内支付。</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>第三条 期限</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>本合同有效期为 6 个</w:t></w:r><w:r><w:t>月，自签署之日起计算。</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>任何一方可提前 30 日书面通知解除合同。</w:t></w:r></w:p>` +
		// A section that is only a table: not an empty section.
		`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>第四条 付款计划</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>项目</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>金额</w:t></w:r></w:p></w:tc></w:tr>` +
		`<w:tr><w:tc><w:p><w:r><w:t>部署</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>USD 500</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`<w:p><w:r><w:t>签署：______________</w:t></w:r></w:p>`
	b := &docBuilder{latin: "Times New Roman", bodyFont: "宋体", headFont: "黑体", size: 12, zh: true}
	b.body.WriteString(body)
	if err := writeZip(path, b.parts(false, "合同", "page")); err != nil {
		t.Fatal(err)
	}
}

// A tracked edit changes two clauses and nothing else: rejecting every
// change gives back the original word for word, the new words carry the old
// words' formatting, and every other part of the file is untouched.
func TestEditDocumentTracked(t *testing.T) {
	t.Setenv("SUPERLEO_NO_OFFICE", "1")
	dir := t.TempDir()
	in, out := filepath.Join(dir, "contract.docx"), filepath.Join(dir, "contract-v2.docx")
	contractDocx(t, in)

	// "USD 500" is written twice (the clause and the table): refused, with both places shown.
	rep := EditDocument(in, out, []DocChange{{Replace: "USD 500", With: "USD 800"}}, DocEditOptions{Track: true})
	mustContain(t, rep.String(), `"USD 500" is written 2 times`, "服务费用为 USD 500", "Give more of the surrounding words")
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a file was written although a change could not be placed")
	}

	rep = EditDocument(in, out, []DocChange{
		{Replace: "费用为 USD 500", With: "费用为 USD 800"},
		{Replace: "6 个月", With: "12 个月"},
		{InsertAfter: "提前 30 日书面通知", Text: "续约须经双方书面确认。"},
	}, DocEditOptions{Track: true, Author: "Liang Li"})
	text := rep.String()
	if !rep.OK() {
		t.Fatalf("the tracked edit reported problems:\n%s", text)
	}
	mustContain(t, text,
		"with tracked changes by Liang Li",
		"tracked changes: ", "deleted by Liang Li", "inserted by Liang Li",
		"with every tracked change rejected, the text is the original, word for word",
		"changed as asked:", "+ 服务费用为 USD 800，交付后 30 日内支付。", "+ 本合同有效期为 12 个月，自签署之日起计算。",
		`paragraph added as asked: "续约须经双方书面确认。"`,
		"styles, headers and footers are byte for byte the original's", "RESULT: OK")

	d, err := openDocx(out)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(d.part("word/document.xml"))
	// The old words are still there, as deletions; the new ones are insertions
	// in the old words' formatting (bold, dark red).
	mustContain(t, doc, `<w:delText`, `w:author="Liang Li"`, `<w:ins `,
		`<w:rPr><w:b/><w:color w:val="C00000"/></w:rPr><w:t xml:space="preserve">800</w:t>`, `<w:delText xml:space="preserve">6</w:delText>`)
	// The table's own "USD 500" was not touched.
	if got := strings.Count(strings.Join(d.viewLines(viewAccepted), "\n"), "USD 500"); got != 1 {
		t.Errorf("USD 500 appears %d times after the edit, want 1 (the table's)", got)
	}
	if !equalLines(d.viewLines(viewRejected), mustLines(t, in)) {
		t.Error("rejecting the changes does not give the original back")
	}
}

func mustLines(t *testing.T, path string) []string {
	t.Helper()
	d, err := openDocx(path)
	if err != nil {
		t.Fatal(err)
	}
	return d.viewLines(viewAccepted)
}

// Without tracking the words are simply replaced, still in their formatting,
// and a paragraph can be removed.
func TestEditDocumentDirect(t *testing.T) {
	t.Setenv("SUPERLEO_NO_OFFICE", "1")
	dir := t.TempDir()
	in, out := filepath.Join(dir, "contract.docx"), filepath.Join(dir, "out.docx")
	contractDocx(t, in)
	rep := EditDocument(in, out, []DocChange{
		{Replace: "6 个月", With: "12 个月"},
		{Delete: "任何一方可提前"},
		{Replace: "USD 500", With: "USD 800", All: true},
	}, DocEditOptions{})
	text := rep.String()
	if !rep.OK() {
		t.Fatalf("problems:\n%s", text)
	}
	mustContain(t, text, "no tracked changes in the edited document", `paragraph removed as asked`, `"USD 500" -> "USD 800" (2 place(s))`)
	lines := strings.Join(mustLines(t, out), "\n")
	if strings.Contains(lines, "USD 500") || strings.Contains(lines, "任何一方") || !strings.Contains(lines, "12 个月，自签署") {
		t.Errorf("the edit did not land:\n%s", lines)
	}
}

// The checker tells a document's real faults from its content.
func TestCheckDocumentFindsWhatIsWrong(t *testing.T) {
	t.Setenv("SUPERLEO_NO_OFFICE", "1")
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.docx")
	bold := func(s string) string { return `<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>` + s + `</w:t></w:r></w:p>` }
	body := bold("一、项目背景") + `<w:p><w:r><w:t>正文内容很多很多很多很多很多很多很多很多。</w:t></w:r></w:p>` +
		bold("二、建设目标") + `<w:p><w:r><w:t>正文内容很多很多很多很多很多很多很多很多。</w:t></w:r></w:p>` +
		bold("三、投资估算") + `<w:p><w:r><w:t>正文内容很多很多很多很多很多很多很多很多。</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>阶段</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`
	b := &docBuilder{latin: "Arial", bodyFont: "宋体", headFont: "黑体", size: 12, zh: true}
	b.body.WriteString(body)
	parts := b.parts(false, "", "none")
	// A style sheet that names no East Asian font anywhere.
	for i, p := range parts {
		if p.name == "word/styles.xml" {
			parts[i].data = []byte(strings.NewReplacer(` w:eastAsia="宋体"`, "", ` w:eastAsia="黑体"`, "").Replace(string(p.data)))
		}
	}
	if err := writeZip(path, parts); err != nil {
		t.Fatal(err)
	}
	rep := CheckDocument(path, DocCheckOptions{Pages: 10})
	mustContain(t, rep.String(),
		"(no headings)",
		"3 paragraph(s) are bold lines standing in for headings",
		"run(s) of Chinese text name no East Asian font",
		"table 1 has a header row and no data",
		"about 10 pages were asked for and the document runs to an estimated 1")
	if rep.OK() {
		t.Fatal("a faulty document passed")
	}

	if rep := CheckDocument(filepath.Join(dir, "missing.docx"), DocCheckOptions{}); rep.OK() {
		t.Error("a file that is not there passed")
	}
	notDocx := filepath.Join(dir, "x.docx")
	_ = os.WriteFile(notDocx, []byte("hello"), 0o644)
	if rep := CheckDocument(notDocx, DocCheckOptions{}); rep.OK() || !strings.Contains(rep.String(), "does not open as a Word document") {
		t.Errorf("a text file renamed .docx: %s", rep)
	}
}

func TestDiffLines(t *testing.T) {
	ops := diffLines(
		[]string{"a", "the fee is 500 dollars", "c", "gone", "e"},
		[]string{"a", "the fee is 800 dollars", "c", "e", "new at the end"})
	want := []lineOp{{"the fee is 500 dollars", "the fee is 800 dollars"}, {"gone", ""}, {"", "new at the end"}}
	if len(ops) != len(want) {
		t.Fatalf("ops: %+v", ops)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("op %d = %+v, want %+v", i, ops[i], want[i])
		}
	}
}

func TestCommonEnds(t *testing.T) {
	cases := []struct {
		a, b     string
		pre, suf int
	}{
		{"费用为 USD 500", "费用为 USD 800", 8, 0},
		{"6 个月", "12 个月", 0, 3},
		{"有效期为六个月", "有效期为十二个月", 4, 2},
		{"version 1.2", "version 1.3", 8, 0},
		{"the old fee", "the new fee", 4, 4},
		{"abc", "abcd", 0, 0},
		{"same", "same", 0, 0},
	}
	for _, c := range cases {
		if pre, suf := commonEnds(c.a, c.b); pre != c.pre || suf != c.suf {
			t.Errorf("commonEnds(%q, %q) = %d, %d; want %d, %d", c.a, c.b, pre, suf, c.pre, c.suf)
		}
	}
}
