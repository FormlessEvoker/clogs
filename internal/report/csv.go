package report

import (
	"encoding/csv"
	"io"
)

type csvWriter struct {
	output io.Writer
	writer *csv.Writer
}

func (w *csvWriter) row(values []string) error {
	if w.writer == nil {
		w.writer = csv.NewWriter(w.output)
	}
	return w.writer.Write(values)
}
func (w *csvWriter) flush() error { w.writer.Flush(); return w.writer.Error() }
