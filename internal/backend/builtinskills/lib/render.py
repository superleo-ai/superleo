"""Turning an office file into page images, for the checkers of every format.

Two ways, tried in this order:

  * LibreOffice, when one that actually runs is on the machine: the file is
    converted to PDF by a real office suite, so what comes out is close to
    what the user will see.
  * Otherwise a preview: the checker hands over the file's content as simple
    HTML and it is laid out into pages here. Enough to see that the content is
    there, in order, and how long it runs; not the file's real pagination.

Either way the pages come back as PNGs and the caller is told which way it was.
"""
import os
import shutil
import subprocess
import sys
import tempfile


def load_fitz():
    """PyMuPDF under its current name, or its old one."""
    try:
        import pymupdf
        return pymupdf
    except ImportError:
        import fitz
        return fitz


def find_soffice():
    """A LibreOffice that actually runs.

    Being on PATH is not enough: Homebrew leaves its `soffice` wrapper behind
    when the app is deleted, and the wrapper then fails on every call.
    """
    if os.environ.get("DOCPY_NO_OFFICE"):
        return None  # for trying the preview path on a machine that has LibreOffice
    candidates = [shutil.which("soffice"), shutil.which("libreoffice"),
                  "/Applications/LibreOffice.app/Contents/MacOS/soffice",
                  "/usr/local/bin/soffice", "/usr/bin/soffice",
                  "C:/Program Files/LibreOffice/program/soffice.exe"]
    for path in candidates:
        if not path or not os.path.exists(path):
            continue
        try:
            done = subprocess.run([path, "--version"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
            if done.returncode == 0:
                return path
        except Exception:
            continue
    return None


MAC_FONT_DIRS = ["/System/Library/Fonts", "/System/Library/Fonts/Supplemental", "/Library/Fonts",
                 os.path.expanduser("~/Library/Fonts")]


def seed_fonts(profile_dir):
    """Hand LibreOffice the machine's fonts through its profile folder.

    Run headless on macOS it does not see the system's fonts at all — only
    the handful it ships — so every Chinese character in a converted file
    comes out as nothing: headings blank, "1月" printed as "1". Fonts placed
    in the profile's own fonts folder are loaded whatever else happens, so the
    system's are linked there. Elsewhere fontconfig finds them and this does
    nothing.
    """
    if sys.platform != "darwin":
        return
    dest = os.path.join(profile_dir, "user", "fonts")
    os.makedirs(dest, exist_ok=True)
    for d in MAC_FONT_DIRS:
        try:
            names = os.listdir(d)
        except OSError:
            continue
        for name in names:
            if name.lower().endswith((".ttf", ".ttc", ".otf")):
                try:
                    os.symlink(os.path.join(d, name), os.path.join(dest, name))
                except OSError:
                    pass


def office_to_pdf(path, out_dir):
    """Convert with LibreOffice; returns the PDF's path, or None."""
    soffice = find_soffice()
    if not soffice:
        return None
    # Its own profile folder: a LibreOffice the user has open would otherwise
    # swallow the command and convert nothing.
    seed_fonts(os.path.join(out_dir, "profile"))
    profile = "-env:UserInstallation=file://" + os.path.join(out_dir, "profile").replace("\\", "/")
    try:
        subprocess.run([soffice, profile, "--headless", "--convert-to", "pdf", "--outdir", out_dir, path],
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=240)
    except Exception:
        return None
    pdf = os.path.join(out_dir, os.path.splitext(os.path.basename(path))[0] + ".pdf")
    return pdf if os.path.exists(pdf) else None


def html_to_pdf(html, pdf_path, landscape=False):
    """Lay simple HTML out into pages. Returns the number of pages."""
    fitz = load_fitz()
    page = fitz.paper_rect("a4-l" if landscape else "a4")
    where = page + (48, 48, -48, -48)
    css = ("body{font-family:sans-serif;font-size:10.5pt;line-height:1.5}"
           "h1{font-size:18pt;margin-top:14pt}h2{font-size:14pt;margin-top:10pt}h3{font-size:12pt}"
           "table{border-collapse:collapse;margin:6pt 0}td,th{border:0.5pt solid #999;padding:2pt 4pt;font-size:9pt}"
           "th{background-color:#e8eef9}.del{color:#b00020;text-decoration:line-through}.ins{color:#0a7a2f;text-decoration:underline}"
           ".cap{font-size:9pt;color:#555}.err{background-color:#ffd6d6}.f{color:#1a4fb5}")
    story = fitz.Story(html=html, user_css=css)
    writer = fitz.DocumentWriter(pdf_path)
    pages = 0
    more = True
    while more and pages < 400:
        dev = writer.begin_page(page)
        more, _ = story.place(where)
        story.draw(dev)
        writer.end_page()
        pages += 1
    writer.close()
    return pages


def pdf_to_pngs(pdf_path, png_dir, prefix="page", zoom=1.5):
    fitz = load_fitz()
    os.makedirs(png_dir, exist_ok=True)
    doc = fitz.open(pdf_path)
    paths = []
    for i, page in enumerate(doc, 1):
        out = os.path.join(png_dir, "%s-%02d.png" % (prefix, i))
        page.get_pixmap(matrix=fitz.Matrix(zoom, zoom), alpha=False).save(out)
        paths.append(out)
    return paths


def render(path, png_dir, html_fallback, prefix="page", landscape=False):
    """PNGs of the file's pages. Returns (how, page_count, paths).

    how is "libreoffice" or "preview". html_fallback is a function returning
    the file's content as HTML, called only when LibreOffice is not there.
    png_dir may be None: the pages are counted and thrown away.
    """
    tmp = tempfile.mkdtemp(prefix="render-")
    try:
        pdf = office_to_pdf(path, tmp)
        how = "libreoffice"
        if pdf is None:
            how = "preview"
            pdf = os.path.join(tmp, "preview.pdf")
            html_to_pdf(html_fallback(), pdf, landscape)
        fitz = load_fitz()
        count = len(fitz.open(pdf))
        paths = pdf_to_pngs(pdf, png_dir, prefix) if png_dir else []
        return how, count, paths
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def esc(text):
    return (str(text).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;"))
