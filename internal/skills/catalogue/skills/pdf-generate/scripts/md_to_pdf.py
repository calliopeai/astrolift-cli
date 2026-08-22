#!/usr/bin/env python3
"""Render a markdown/text report to PDF with reportlab.

Supports a practical markdown subset — ``#``/``##``/``###`` headings,
paragraphs, and ``-``/``*`` bullet lists — which covers most report-style
deliverables. Output is a paginated US-Letter PDF.

Dependency: ``reportlab`` (``pip install reportlab``) — pure-Python, no
system libraries, so it runs anywhere. For full HTML/CSS fidelity use
``weasyprint`` instead (needs Cairo/Pango system libs).

Examples::

    python md_to_pdf.py report.md report.pdf
    python md_to_pdf.py - report.pdf --title "Q3 Summary" < report.md
"""

from __future__ import annotations

import argparse
import sys
from xml.sax.saxutils import escape


def _read_input(path: str) -> str:
    if path == "-":
        return sys.stdin.read()
    with open(path, encoding="utf-8") as fh:
        return fh.read()


def build_flowables(markdown: str, styles):
    """Translate the markdown subset into a list of reportlab flowables."""
    from reportlab.platypus import ListFlowable, ListItem, Paragraph, Spacer

    flowables = []
    paragraph: list[str] = []
    bullets: list[str] = []

    def flush_paragraph():
        if paragraph:
            flowables.append(Paragraph(escape(" ".join(paragraph)), styles["BodyText"]))
            flowables.append(Spacer(1, 6))
            paragraph.clear()

    def flush_bullets():
        if bullets:
            items = [ListItem(Paragraph(escape(b), styles["BodyText"])) for b in bullets]
            flowables.append(ListFlowable(items, bulletType="bullet", leftIndent=18))
            flowables.append(Spacer(1, 6))
            bullets.clear()

    for raw in markdown.splitlines():
        line = raw.rstrip()
        stripped = line.strip()
        if not stripped:
            flush_paragraph()
            flush_bullets()
            continue
        if stripped.startswith("### "):
            flush_paragraph()
            flush_bullets()
            flowables.append(Paragraph(escape(stripped[4:]), styles["Heading3"]))
        elif stripped.startswith("## "):
            flush_paragraph()
            flush_bullets()
            flowables.append(Paragraph(escape(stripped[3:]), styles["Heading2"]))
        elif stripped.startswith("# "):
            flush_paragraph()
            flush_bullets()
            flowables.append(Paragraph(escape(stripped[2:]), styles["Heading1"]))
        elif stripped.startswith(("- ", "* ")):
            flush_paragraph()
            bullets.append(stripped[2:])
        else:
            flush_bullets()
            paragraph.append(stripped)

    flush_paragraph()
    flush_bullets()
    return flowables


def render(markdown: str, out_path: str, *, title: str | None) -> None:
    try:
        from reportlab.lib.pagesizes import letter
        from reportlab.lib.styles import getSampleStyleSheet
        from reportlab.platypus import Paragraph, SimpleDocTemplate, Spacer
    except ImportError:  # pragma: no cover - exercised only without reportlab
        raise SystemExit("pdf-generate requires reportlab — run: pip install reportlab")

    styles = getSampleStyleSheet()
    doc = SimpleDocTemplate(
        out_path,
        pagesize=letter,
        topMargin=54,
        bottomMargin=54,
        leftMargin=54,
        rightMargin=54,
        title=title or "",
    )
    flowables = []
    if title:
        flowables.append(Paragraph(escape(title), styles["Title"]))
        flowables.append(Spacer(1, 12))
    flowables.extend(build_flowables(markdown, styles))
    doc.build(flowables)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Render markdown/text to PDF.")
    parser.add_argument("input", help="markdown/text file, or '-' for stdin")
    parser.add_argument("output", help="output .pdf path")
    parser.add_argument("--title", help="document title (rendered + PDF metadata)")
    args = parser.parse_args(argv)

    markdown = _read_input(args.input)
    render(markdown, args.output, title=args.title)
    print(f"wrote {args.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
