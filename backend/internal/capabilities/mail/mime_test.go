package mail

import (
	"net/mail"
	"strings"
	"testing"
)

// Reading a real message out of a real .eml.
//
// The two failures this covers were both visible on screen: an umlaut arriving
// as "=E4" because the transfer encoding was never undone, and an HTML mail
// showing nothing at all because the parts that are not plain text were
// dropped. Attachments were dropped with them.

const message = "From: Studienbuero <buero@dhbw-ravensburg.de>\r\n" +
	"To: feuerstein.leon-25@lehre.dhbw-ravensburg.de\r\n" +
	"Subject: =?UTF-8?Q?Pr=C3=BCfungsanmeldung?=\r\n" +
	"Date: Mon, 4 Aug 2025 09:12:00 +0200\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"outer\"\r\n" +
	"\r\n" +
	"--outer\r\n" +
	"Content-Type: multipart/alternative; boundary=\"inner\"\r\n" +
	"\r\n" +
	"--inner\r\n" +
	"Content-Type: text/plain; charset=UTF-8\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"Sehr geehrte Studierende, die Pr=C3=BCfung f=C3=A4llt aus.\r\n" +
	"--inner\r\n" +
	"Content-Type: text/html; charset=UTF-8\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"PHA+RGllIFByw7xmdW5nIGbDpGxsdCBhdXMuPC9wPg==\r\n" +
	"--inner--\r\n" +
	"--outer\r\n" +
	"Content-Type: application/pdf; name=\"Anmeldung.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"Anmeldung.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"JVBERi0xLjQKJcOkw7zDtsOfCg==\r\n" +
	"--outer--\r\n"

func TestAMessageIsReadTheWayItWasWritten(t *testing.T) {
	parsed, err := mail.ReadMessage(strings.NewReader(message))
	if err != nil {
		t.Fatalf("the message could not be read: %v", err)
	}

	if got := decodeHeader(parsed.Header.Get("Subject")); got != "Prüfungsanmeldung" {
		t.Errorf("subject came out as %q", got)
	}

	text, html, files := readMessage(parsed)

	// This is the failure that was on screen: "=C3=A4" instead of "ä".
	if !strings.Contains(text, "Prüfung fällt aus") {
		t.Errorf("the text is still encoded: %q", text)
	}
	if strings.Contains(text, "=C3") {
		t.Errorf("quoted-printable was not undone: %q", text)
	}
	if !strings.Contains(html, "<p>Die Prüfung fällt aus.</p>") {
		t.Errorf("the html part was not decoded: %q", html)
	}

	if len(files) != 1 {
		t.Fatalf("wanted one attachment, got %d", len(files))
	}
	if files[0].Filename != "Anmeldung.pdf" || files[0].Type != "application/pdf" {
		t.Errorf("the attachment came out as %+v", files[0])
	}
	if !strings.HasPrefix(string(files[0].data), "%PDF-1.4") {
		t.Errorf("the attachment is not the file that was sent: %q", files[0].data[:8])
	}
	if files[0].Index != 0 {
		t.Errorf("the attachment has no stable position: %d", files[0].Index)
	}
}

// A message in the charset half of Europe still uses: it has to arrive as UTF-8
// or every German mail from an older system is unreadable.
func TestAnOlderCharsetIsBroughtOver(t *testing.T) {
	raw := "Subject: hallo\r\n" +
		"Content-Type: text/plain; charset=ISO-8859-1\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"Gr=FC=DFe aus Ravensburg\r\n"
	parsed, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("the message could not be read: %v", err)
	}
	text, _, _ := readMessage(parsed)
	if !strings.Contains(text, "Grüße aus Ravensburg") {
		t.Errorf("latin-1 was not brought over: %q", text)
	}
}

// A message that is only plain text must not lose its body to the new walk.
func TestPlainTextStillArrives(t *testing.T) {
	raw := "Subject: plain\r\n\r\njust a line\r\n"
	parsed, _ := mail.ReadMessage(strings.NewReader(raw))
	text, html, files := readMessage(parsed)
	if !strings.Contains(text, "just a line") {
		t.Errorf("the body was lost: %q", text)
	}
	if html != "" || len(files) != 0 {
		t.Errorf("something was invented: html=%q files=%d", html, len(files))
	}
}
