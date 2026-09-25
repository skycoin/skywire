package skymail

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A message shaped like Thunderbird's: alternative text/html bodies,
// base64 wrapped at 76 columns, and an attachment.
const multipartMsg = "From: =?utf-8?q?J=C3=BCrgen?= <j@example.com>\r\n" +
	"To: me@example.com\r\n" +
	"Subject: =?utf-8?b?w5xiZXIgc2t5bmV0?=\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"outer\"\r\n" +
	"\r\n" +
	"--outer\r\n" +
	"Content-Type: multipart/alternative; boundary=\"inner\"\r\n" +
	"\r\n" +
	"--inner\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"cGxhaW4gYm9keSDDvGJlciBza3luZXQ=\r\n" +
	"--inner\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<p>html body</p>\r\n" +
	"--inner--\r\n" +
	"--outer\r\n" +
	"Content-Type: application/pdf; name=\"doc.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"JVBERi0xLjQK\r\n" +
	"--outer--\r\n"

func TestRenderPrefersPlainTextAndListsAttachments(t *testing.T) {
	r, err := Render([]byte(multipartMsg))
	require.NoError(t, err)
	require.Equal(t, "Über skynet", r.Subject)
	require.Equal(t, "Jürgen <j@example.com>", r.From)
	require.Equal(t, "plain body über skynet", r.Text)
	require.False(t, r.FromHTML)
	require.Len(t, r.Attachments, 1)
	require.Equal(t, Attachment{Name: "doc.pdf", ContentType: "application/pdf", Size: 9}, r.Attachments[0])
	require.False(t, r.Verified, "no transport stamp, nothing verified")
}

func TestRenderReducesHTMLOnlyMailToText(t *testing.T) {
	raw := "Subject: x\r\nContent-Type: text/html\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"<html><head><style>p{}</style></head><body><script>alert(1)</script>" +
		"<p>one &amp; two</p><p>th=\r\nree</p></body></html>\r\n"
	r, err := Render([]byte(raw))
	require.NoError(t, err)
	require.True(t, r.FromHTML)
	require.Equal(t, "one & two\nthree", r.Text)
	require.NotContains(t, r.Text, "alert")
	require.False(t, strings.Contains(r.Text, "<"))
}
