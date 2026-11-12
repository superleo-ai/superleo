package backend

import (
	"bytes"
	"embed"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Skills that ship inside the binary.
//
// Asked for a slide deck, SuperLeo answered that it had no presentation tool
// and stopped; asked to change two figures in a contract PDF, it wrote a
// one-off script that changed the font and wiped a heading, and reported the
// rest untouched. Both are know-how it did not have, and ~/.superleo/skills —
// the only place skills came from — is empty until somebody installs one.
//
// So the document skills are part of the product: SKILL.md plus the scripts it
// tells the model to run, embedded here and written out at start. They go to
// their own folder, <data>/builtin-skills, not into <data>/skills:
//
//   - <data>/skills is the person's. What is in it they installed and can
//     remove, the queen hands it to her workers, and remove_skill deletes from
//     it. A built-in dropped in there would be handed round and deleted like
//     one of theirs, then come back at the next start.
//   - The folder is rewritten from the binary on every start, so an upgrade
//     upgrades the skills. Nothing a person edits belongs in a folder that
//     does that.
//
// They share one shape, so that the next format is added the same way and the
// model meets the same steps whatever it is making:
//
//   - file-check is the general one: any file, opened as what its bytes say it
//     is, printed back, compared with the file it was made from. It is the
//     skill behind agent-go's "check the file you produced" step, which knows
//     nothing about formats.
//   - A format gets a skill of its own when making it well takes more than a
//     few lines. Each has something that makes or changes the file and a
//     checker that reads it back, and its SKILL.md runs read → make → check →
//     report.
//   - Every checker ends "RESULT: OK" or "RESULT: N problem(s)" and prints one
//     "PROBLEM:" line per fault.
//
// Where the hands are differs, and is changing. docx and xlsx are tools
// compiled into this binary (internal/office, registered in
// office_tools.go): their skills are instructions only. pdf, ppt and
// file-check still run Python scripts shipped beside their SKILL.md, through
// builtinskills/docpy, which builds an environment with uv on first use. That
// is a second runtime to fetch and keep working, it has no answer on a clean
// Windows machine, and it is against the rule that what SuperLeo depends on
// is a native binary — so those three are to move into internal/office the
// same way, and docpy goes when the last of them has.
//
// A skill of the same name in <data>/skills wins: agent-go keeps the first one
// it finds and the built-in folder is searched last (see builtinSkillPaths).
//
//go:embed all:builtinskills
var builtinSkillsFS embed.FS

// builtinSkillsRootToken is replaced, in every embedded text file, by the
// folder the skills were written to. A skill's instructions have to name its
// scripts by absolute path — the model runs them from the workspace — and that
// path is only known on the machine.
const builtinSkillsRootToken = "{{SKILLS_ROOT}}"

// BuiltinSkillsDir is where the embedded skills are written.
func BuiltinSkillsDir() string { return filepath.Join(DataDir(), "builtin-skills") }

// installBuiltinSkills writes the embedded skills to BuiltinSkillsDir,
// replacing what an earlier version left there, and returns the folder. Files
// already identical are left alone, so a start does not touch the disk for
// nothing. On failure it returns "" and the agent runs without them.
func installBuiltinSkills() string {
	root := BuiltinSkillsDir()
	if err := writeBuiltinSkills(root); err != nil {
		slog.Warn("built-in skills not installed", "dir", root, "err", err)
		return ""
	}
	return root
}

func writeBuiltinSkills(root string) error {
	sub, err := fs.Sub(builtinSkillsFS, "builtinskills")
	if err != nil {
		return err
	}
	kept := map[string]bool{}
	err = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		dst := filepath.Join(root, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		raw, err := fs.ReadFile(sub, path)
		if err != nil {
			return err
		}
		raw = bytes.ReplaceAll(raw, []byte(builtinSkillsRootToken), []byte(filepath.ToSlash(root)))
		kept[dst] = true
		// Scripts are run directly; embed does not keep the mode bits.
		mode := os.FileMode(0o644)
		if builtinSkillIsScript(path) {
			mode = 0o755
		}
		if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, raw) {
			return os.Chmod(dst, mode)
		}
		if err := os.WriteFile(dst, raw, mode); err != nil {
			return err
		}
		return os.Chmod(dst, mode)
	})
	if err != nil {
		return err
	}
	// A file a newer version no longer ships must not linger: a renamed
	// script would otherwise stay runnable beside its replacement, and a
	// dropped skill would stay loaded. Only inside skill folders the binary
	// knows.
	entries, _ := fs.ReadDir(sub, ".")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return nil
			}
			if !kept[path] && !strings.Contains(path, "__pycache__") {
				_ = os.Remove(path)
			}
			return nil
		})
	}
	return nil
}

func builtinSkillIsScript(path string) bool {
	if strings.HasSuffix(path, ".py") || strings.HasSuffix(path, ".sh") {
		return true
	}
	return filepath.Base(path) == "docpy"
}

// builtinSkillPaths is where agent-go looks for skills: its own defaults for
// this data folder first — the person's skills — then the built-in ones, so an
// installed skill shadows a built-in of the same name.
func builtinSkillPaths(defaults []string) []string {
	paths := append([]string(nil), defaults...)
	if root := installBuiltinSkills(); root != "" {
		paths = append(paths, root)
	}
	return paths
}
