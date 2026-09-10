// Package logging pkg/logging/formatter_bench_test.go c0-com-log
package logging

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
)

// benchFormat drives Format the way logrus does on a live logger: the entry
// carries a pooled buffer that is reset between writes.
func benchFormat(b *testing.B, f *TextFormatter, entry *logrus.Entry) {
	buf := &bytes.Buffer{}
	entry.Buffer = buf

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		if _, err := f.Format(entry); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFormat covers the shapes a dmsg-server actually emits: a bare
// message, one or two fields, and an error field, uncolored (stderr is a
// pipe under systemd) as well as colored.
func BenchmarkFormat(b *testing.B) {
	cases := []struct {
		name  string
		newf  func() *TextFormatter
		entry *logrus.Entry
	}{
		{"plain/no-fields", prodFormatter, goldenEntry(logrus.InfoLevel, "serving", moduleFields("dmsg_server", nil))},
		{"plain/one-field", prodFormatter, goldenEntry(logrus.InfoLevel, "stream closed", moduleFields("dmsg_server", logrus.Fields{"dst": "abc"}))},
		{"plain/three-fields", prodFormatter, goldenEntry(logrus.InfoLevel, "stream closed", moduleFields("dmsg_server", logrus.Fields{"dst": "abc", "src": "def", "size": 4096}))},
		{"plain/error-field", prodFormatter, goldenEntry(logrus.ErrorLevel, "dial failed", moduleFields("dmsg_server", logrus.Fields{logrus.ErrorKey: errors.New("connection refused")}))},
		{"colored/no-fields", coloredFormatter, goldenEntry(logrus.InfoLevel, "serving", moduleFields("dmsg_server", nil))},
		{"colored/three-fields", coloredFormatter, goldenEntry(logrus.InfoLevel, "stream closed", moduleFields("dmsg_server", logrus.Fields{"dst": "abc", "src": "def", "size": 4096}))},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) { benchFormat(b, c.newf(), c.entry) })
	}
}
