---
name: pdf-generate
description: "Print to PDF" — render a markdown/text report (headings, paragraphs, bullet lists) to a clean PDF document.
version: "0.1.0"
---

# pdf-generate

Turn a report into a PDF. Use this when a task asks for a PDF deliverable — a
summary, a report, an invoice-like document — rather than leaving the output as
plain text.

The helper at `scripts/md_to_pdf.py` uses **`reportlab`** (`pip install reportlab`)
because it is pure-Python and needs no system libraries — it works in any runtime
out of the box. It renders a practical subset of markdown: `#`/`##`/`###`
headings, paragraphs, and `-`/`*` bullet lists.

**If you need full HTML/CSS fidelity** (complex layout, web pages), use
`weasyprint` instead (`pip install weasyprint`) — note it requires system
libraries (Cairo, Pango), so the runtime must have those; prefer the reportlab
helper when your input is a report you control.

## Using the helper

```bash
# Markdown file -> PDF
python scripts/md_to_pdf.py report.md report.pdf

# From stdin, with a document title
python scripts/md_to_pdf.py - report.pdf --title "Q3 Summary" < report.md
```

The input is markdown (or plain text — plain text just becomes paragraphs). The
output is a paginated PDF on US Letter with sensible margins.

## Procedure

1. **Compose the content as markdown first.** Get the structure right in text —
   headings for sections, bullets for lists — before rendering. It's far easier
   to iterate on markdown than on a PDF.
2. **Render** with the helper to your target path.
3. **Confirm the file exists and is non-empty** (`ls -l report.pdf`) — a 0-byte
   PDF means rendering failed.

## Supported markdown subset

| Markdown | Renders as |
|---|---|
| `# Title` | large heading |
| `## Section` / `### Sub` | smaller headings |
| `- item` or `* item` | bullet list item |
| blank-line-separated text | paragraphs |

Anything fancier (tables, images, inline emphasis) is rendered as plain text. If
you need those, pre-render to HTML and use `weasyprint`.

## Cautions

- Declare the dependency: the runner must `pip install reportlab` (or
  `weasyprint`) before this runs — it is **not** stdlib.
- Very long unbroken lines are wrapped by the PDF engine, but giant tables won't
  lay out well in the markdown path — use the HTML route for those.
