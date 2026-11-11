// Package logging pkg/logging/json.go c0-com-log
package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// JSONFormatter renders an entry as one JSON object per line: the fields
// plus "time", "msg" and "level", keys sorted. A field named time, msg or
// level is kept as "fields.<name>". This is the layout logrus' JSONFormatter
// wrote, which the visor's JSON log file and log store readers expect.
type JSONFormatter struct {
	// TimestampFormat is the layout for "time". Empty means time.RFC3339.
	TimestampFormat string
	// DisableTimestamp leaves "time" out.
	DisableTimestamp bool
	// DisableHTMLEscape keeps <, > and & as they are in strings.
	DisableHTMLEscape bool
}

// Format implements Formatter.
func (f *JSONFormatter) Format(e *Entry) ([]byte, error) {
	data := make(map[string]any, len(e.Data)+3)
	for k, v := range e.Data {
		if err, ok := v.(error); ok {
			v = err.Error() // encoding/json renders an error as {}
		}
		data[k] = v
	}
	for _, k := range [...]string{"time", "msg", "level"} {
		if v, ok := data[k]; ok {
			data["fields."+k] = v
			delete(data, k)
		}
	}
	if !f.DisableTimestamp {
		layout := f.TimestampFormat
		if layout == "" {
			layout = time.RFC3339
		}
		data["time"] = e.Time.Format(layout)
	}
	data["msg"] = e.Message
	data["level"] = e.Level.String()

	b := e.Buffer
	if b == nil {
		b = new(bytes.Buffer)
	}
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(!f.DisableHTMLEscape)
	if err := enc.Encode(data); err != nil {
		return nil, fmt.Errorf("failed to marshal fields to JSON, %w", err)
	}
	return b.Bytes(), nil
}
