package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/skills"
)

// The built-in skills are written out with their scripts runnable and with
// the folder they landed in spelled into the instructions: the model runs the
// scripts from the workspace and has only the path the SKILL.md gives it.
func TestBuiltinSkillsAreWrittenOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)

	root := installBuiltinSkills()
	if root != filepath.Join(home, "builtin-skills") {
		t.Fatalf("installed to %q", root)
	}
	// The skills whose hands are still scripts beside them; docx and xlsx are
	// instructions for tools compiled into the binary.
	scripted := map[string]bool{"file-check": true, "pdf": true, "ppt": true}
	for _, name := range []string{"file-check", "pdf", "ppt", "docx", "xlsx"} {
		raw, err := os.ReadFile(filepath.Join(root, name, "SKILL.md"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := string(raw)
		if strings.Contains(text, builtinSkillsRootToken) {
			t.Errorf("%s: the folder token was left in the instructions", name)
		}
		if scripted[name] && !strings.Contains(text, filepath.ToSlash(root)+"/docpy") {
			t.Errorf("%s: the instructions do not give the path of docpy", name)
		}
		if !scripted[name] {
			// Nothing to run but the tool: no interpreter, no script, no install.
			for _, word := range []string{"docpy", "python3", "pip install", "uv "} {
				if strings.Contains(text, word) {
					t.Errorf("%s: a skill for a built-in tool tells the model to use %q", name, word)
				}
			}
			tool := map[string]string{"docx": "word_document", "xlsx": "excel_workbook"}[name]
			if !strings.Contains(text, "`"+tool+"`") {
				t.Errorf("%s: the instructions do not name the %s tool", name, tool)
			}
		}
		// Every script the instructions name is there to be run.
		for _, word := range strings.Fields(text) {
			if !strings.HasPrefix(word, filepath.ToSlash(root)) || !strings.HasSuffix(word, ".py") {
				continue
			}
			info, err := os.Stat(word)
			if err != nil {
				t.Errorf("%s names %s, which was not written", name, word)
			} else if info.Mode()&0o111 == 0 {
				t.Errorf("%s is not executable", word)
			}
		}
	}
	if info, err := os.Stat(filepath.Join(root, "docpy")); err != nil || info.Mode()&0o111 == 0 {
		t.Errorf("docpy is missing or not executable: %v", err)
	}

	// A file an older version shipped is gone after the next start; a file
	// that changed is put back.
	stale := filepath.Join(root, "pdf", "scripts", "old_tool.py")
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(root, "pdf", "SKILL.md")
	if err := os.WriteFile(edited, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	installBuiltinSkills()
	if _, err := os.Stat(stale); err == nil {
		t.Error("a script no longer shipped was left runnable")
	}
	if raw, _ := os.ReadFile(edited); string(raw) == "changed" {
		t.Error("a built-in skill was not restored from the binary")
	}
}

// The skills load, and the requests they exist for find them — in the words
// people use, which here are mostly Chinese.
func TestBuiltinSkillsAreFoundByTheRequestsTheyAreFor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	root := installBuiltinSkills()

	cfg := skills.DefaultConfig()
	cfg.Paths = []string{filepath.Join(home, "skills"), root}
	cfg.DBPath = filepath.Join(home, "skills.db")
	svc, err := skills.NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if err := svc.LoadAll(context.Background()); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"写一份「2026 年中国储能行业」的行业研究报告 PPT，15 页左右，存成 uploads/ppttest/energy-storage-2026.pptx": "ppt",
		"帮我做一个介绍公司产品的幻灯片":                                                     "ppt",
		"把 uploads/pdftest/contract.pdf 里的费用从 USD 500 改成 USD 800，其他不动，版式保持一致": "pdf",
		"把这两份合同合并成一个文件再加水印":                                                   "pdf",
		"Make a slide deck about our Q3 results":                              "ppt",
		"导出成 Excel 之后帮我核对一下生成的文件有没有改错":                                        "file-check",
		"写一份 10 页左右的项目方案，要有目录和表格，存成 uploads/doctest/plan.docx":                "docx",
		"把合同里的两处条款改掉，用修订模式，让我能看到改了哪里":                                         "docx",
		"做一个 2026 年月度预算表，要有公式、合计、条件格式和图表，存成 budget-2026.xlsx":                 "xlsx",
		"把 uploads/xlsxtest/sales-raw.csv 清洗一下，去重、统一日期格式，再出一张汇总表":             "xlsx",
		"Write a project proposal as a Word document":                         "docx",
		"Build an Excel budget workbook with formulas":                        "xlsx",
	}
	for query, want := range cases {
		got, err := svc.ResolveForModelScored(context.Background(), query, nil, skills.DefaultSkillMinScore)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 || got[0].Skill.ID != want {
			names := []string{}
			for _, g := range got {
				names = append(names, g.Skill.ID)
			}
			t.Errorf("%q found %v, want %s first", query, names, want)
		}
	}

	// And a request that has nothing to do with files finds none of them.
	for _, query := range []string{"明天上海天气怎么样", "What is on my calendar tomorrow?", "帮我记一下下午三点开会"} {
		got, _ := svc.ResolveForModelScored(context.Background(), query, nil, skills.DefaultSkillMinScore)
		if len(got) != 0 {
			t.Errorf("%q found skill %s (%.2f); it should find none", query, got[0].Skill.ID, got[0].Score)
		}
	}
}
