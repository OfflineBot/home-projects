package mail

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"

	"golang.org/x/net/html/charset"
)

// Just enough MIME to show a message: decoded headers and the text part.

var headerDecoder = mime.WordDecoder{}

func decodeHeader(v string) string {
	if v == "" {
		return ""
	}
	decoded, err := headerDecoder.DecodeHeader(v)
	if err != nil {
		return v
	}
	return decoded
}

func encodeHeader(v string) string {
	for _, r := range v {
		if r > 127 {
			return mime.QEncoding.Encode("utf-8", v)
		}
	}
	return v
}

// A message is read the way a mail program reads one.
//
// Two things were missing and both showed: the transfer encoding was never
// undone, so every umlaut arrived as "=E4" and a base64 body as a wall of
// letters; and the parts that are files rather than text were dropped on the
// floor. What the server delivered stays untouched in the .eml — this is only
// how it is read out of it.

// Part is one attachment: what it is called, what it is, and where to find it
// again (its position in the message, counted the same way every time).
type Part struct {
	Index     int    `json:"index"`
	Filename  string `json:"filename"`
	Type      string `json:"type"`
	Size      int    `json:"size"`
	ContentID string `json:"contentId,omitempty"`
	Inline    bool   `json:"inline,omitempty"`
	data      []byte
}

// read undoes the transfer encoding and brings the text to UTF-8. Without
// this a German mail is unreadable and an HTML mail is base64.
func read(header textproto.MIMEHeader, r io.Reader, mediaType string, params map[string]string) []byte {
	var reader io.Reader = io.LimitReader(r, 32<<20)
	switch strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, stripSpace(reader))
	case "quoted-printable":
		reader = quotedprintable.NewReader(reader)
	}
	body, _ := io.ReadAll(reader)
	if !strings.HasPrefix(mediaType, "text/") {
		return body
	}
	if set := params["charset"]; set != "" && !strings.EqualFold(set, "utf-8") {
		if converted, err := charset.NewReaderLabel(set, bytes.NewReader(body)); err == nil {
			if out, rerr := io.ReadAll(converted); rerr == nil {
				return out
			}
		}
	}
	return body
}

// stripSpace lets base64 survive the line breaks every mail server puts in it.
func stripSpace(r io.Reader) io.Reader {
	return transformReader{r: bufio.NewReader(r)}
}

type transformReader struct{ r *bufio.Reader }

func (t transformReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		b, err := t.r.ReadByte()
		if err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		if b == '\r' || b == '\n' || b == ' ' || b == '\t' {
			continue
		}
		p[n] = b
		n++
	}
	return n, nil
}

func extractBodies(msg *mail.Message) (text string, html string) {
	text, html, _ = readMessage(msg)
	return text, html
}

// readMessage is the whole of it: the text, the HTML, and the files.
func readMessage(msg *mail.Message) (text string, html string, parts []Part) {
	header := textproto.MIMEHeader(msg.Header)
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		body, _ := io.ReadAll(io.LimitReader(msg.Body, 32<<20))
		return string(body), "", nil
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		state := &walk{}
		state.parts(msg.Body, params["boundary"], 0)
		return state.text, state.html, state.files
	}
	body := read(header, msg.Body, mediaType, params)
	if mediaType == "text/html" {
		return "", string(body), nil
	}
	return string(body), "", nil
}

// walk carries what has been found so far through the nesting. Mails nest:
// mixed around alternative around related, and the text can be three levels in.
type walk struct {
	text  string
	html  string
	files []Part
}

func (w *walk) parts(r io.Reader, boundary string, depth int) {
	if boundary == "" || depth > 6 {
		return
	}
	mr := multipart.NewReader(r, boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		mediaType, params, perr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if perr != nil {
			mediaType, params = "application/octet-stream", map[string]string{}
		}
		disposition, dparams, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		name := decodeHeader(firstOf(dparams["filename"], params["name"]))

		switch {
		case strings.HasPrefix(mediaType, "multipart/"):
			w.parts(part, params["boundary"], depth+1)
		case mediaType == "text/plain" && w.text == "" && name == "":
			w.text = string(read(part.Header, part, mediaType, params))
		case mediaType == "text/html" && w.html == "" && name == "":
			w.html = string(read(part.Header, part, mediaType, params))
		default:
			// Everything else is a file: an attachment, or an image the HTML
			// refers to by its content id.
			body := read(part.Header, part, mediaType, params)
			if len(body) == 0 {
				continue
			}
			if name == "" {
				name = "part-" + strconv.Itoa(len(w.files)+1) + extensionFor(mediaType)
			}
			w.files = append(w.files, Part{
				Index:     len(w.files),
				Filename:  name,
				Type:      mediaType,
				Size:      len(body),
				ContentID: strings.Trim(part.Header.Get("Content-Id"), "<>"),
				Inline:    strings.EqualFold(disposition, "inline"),
				data:      body,
			})
		}
	}
}

func firstOf(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func extensionFor(mediaType string) string {
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ""
}

func jsonUnmarshal(raw []byte, out any) error { return json.Unmarshal(raw, out) }
