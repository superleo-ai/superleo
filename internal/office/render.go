package office

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Laying a document out, when something on the machine can.
//
// How many pages a document runs to, and what each looks like, is decided by
// the program that lays it out. Nothing compiled into this binary does that.
// LibreOffice does, and when one that actually runs is installed it is asked
// to convert the file to PDF: the page count is read from that, and the PDF
// can be kept for a person to open. It is never required. Without it the
// checkers work from the file's structure and say that pages were estimated.

// findOffice returns a LibreOffice that actually runs, or "".
//
// Being on PATH is not enough: Homebrew leaves its `soffice` wrapper behind
// when the app is deleted, and the wrapper then fails on every call.
func findOffice() string {
	if os.Getenv("SUPERLEO_NO_OFFICE") != "" {
		return ""
	}
	candidates := []string{}
	for _, name := range []string{"soffice", "libreoffice"} {
		if p, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, p)
		}
	}
	candidates = append(candidates,
		"/Applications/LibreOffice.app/Contents/MacOS/soffice",
		"/usr/bin/soffice", "/usr/local/bin/soffice",
		`C:\Program Files\LibreOffice\program\soffice.exe`,
		`C:\Program Files (x86)\LibreOffice\program\soffice.exe`)
	for _, p := range candidates {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		cmd := exec.Command(p, "--version")
		done := make(chan error, 1)
		if cmd.Start() != nil {
			continue
		}
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err == nil {
				return p
			}
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
	return ""
}

// seedFonts hands LibreOffice the machine's fonts through its profile folder.
//
// Run headless on macOS it does not see the system's fonts at all — only the
// handful it ships — so every Chinese character in a converted file comes out
// as nothing: headings blank, "1月" printed as "1". Fonts in the profile's
// own fonts folder are loaded whatever else happens, so the system's are
// linked there. Elsewhere fontconfig finds them and this does nothing.
func seedFonts(profile string) {
	if runtime.GOOS != "darwin" {
		return
	}
	dest := filepath.Join(profile, "user", "fonts")
	if os.MkdirAll(dest, 0o755) != nil {
		return
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/System/Library/Fonts", "/System/Library/Fonts/Supplemental", "/Library/Fonts", filepath.Join(home, "Library", "Fonts")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			switch strings.ToLower(filepath.Ext(e.Name())) {
			case ".ttf", ".ttc", ".otf":
				_ = os.Symlink(filepath.Join(dir, e.Name()), filepath.Join(dest, e.Name()))
			}
		}
	}
}

var pdfPageRe = regexp.MustCompile(`/Type\s*/Page[^s]`)

// layOut converts a file to PDF with LibreOffice. It returns the page count
// and, when keepDir is not empty, the path of the PDF kept there. ok is false
// when there is no LibreOffice or the conversion failed.
func layOut(path, keepDir string) (pages int, pdf string, ok bool) {
	office := findOffice()
	if office == "" {
		return 0, "", false
	}
	tmp, err := os.MkdirTemp("", "superleo-layout-")
	if err != nil {
		return 0, "", false
	}
	defer os.RemoveAll(tmp)
	// Its own profile folder: a LibreOffice the person has open would
	// otherwise swallow the command and convert nothing.
	profile := filepath.Join(tmp, "profile")
	seedFonts(profile)
	cmd := exec.Command(office, "-env:UserInstallation=file://"+filepath.ToSlash(profile), "--headless",
		"--convert-to", "pdf", "--outdir", tmp, path)
	done := make(chan error, 1)
	if cmd.Start() != nil {
		return 0, "", false
	}
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(4 * time.Minute):
		_ = cmd.Process.Kill()
		return 0, "", false
	}
	made := filepath.Join(tmp, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".pdf")
	raw, err := os.ReadFile(made)
	if err != nil {
		return 0, "", false
	}
	pages = len(pdfPageRe.FindAll(raw, -1))
	if keepDir != "" && os.MkdirAll(keepDir, 0o755) == nil {
		pdf = filepath.Join(keepDir, filepath.Base(made))
		if os.WriteFile(pdf, raw, 0o644) != nil {
			pdf = ""
		}
	}
	return pages, pdf, pages > 0
}
