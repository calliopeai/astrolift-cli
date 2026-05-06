package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
)

// Format represents the output format for CLI responses.
type Format int

const (
	FormatHuman Format = iota
	FormatJSON
)

// Writer handles formatted output to stdout/stderr.
type Writer struct {
	out    io.Writer
	errOut io.Writer
	format Format
	color  bool
}

// New creates a Writer that writes data to stdout and errors to stderr.
func New(format Format, color bool) *Writer {
	return &Writer{
		out:    os.Stdout,
		errOut: os.Stderr,
		format: format,
		color:  color,
	}
}

// JSON writes the value as indented JSON to stdout.
func (w *Writer) JSON(v interface{}) error {
	enc := json.NewEncoder(w.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Table prints rows as an aligned table. The first row is treated as
// the header.
func (w *Writer) Table(headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w.out, 0, 0, 2, ' ', 0)

	for i, h := range headers {
		if i > 0 {
			fmt.Fprint(tw, "\t")
		}
		fmt.Fprint(tw, h)
	}
	fmt.Fprintln(tw)

	for _, row := range rows {
		for i, col := range row {
			if i > 0 {
				fmt.Fprint(tw, "\t")
			}
			fmt.Fprint(tw, col)
		}
		fmt.Fprintln(tw)
	}

	tw.Flush()
}

// Error prints a formatted error message to stderr in the style:
//
//	error: <summary>
//	       <hint>
func (w *Writer) Error(summary string, hint string) {
	fmt.Fprintf(w.errOut, "error: %s\n", summary)
	if hint != "" {
		fmt.Fprintf(w.errOut, "       %s\n", hint)
	}
}

// Print writes a line to stdout.
func (w *Writer) Print(msg string) {
	fmt.Fprintln(w.out, msg)
}
