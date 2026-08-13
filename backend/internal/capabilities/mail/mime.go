package mail

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
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

// extractBodies returns the plain-text and the HTML part of a message.
func extractBodies(msg *mail.Message) (text string, html string) {
	contentType := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		body, _ := io.ReadAll(io.LimitReader(msg.Body, 4<<20))
		return string(body), ""
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		return walkParts(msg.Body, params["boundary"], 0)
	}
	body, _ := io.ReadAll(io.LimitReader(msg.Body, 4<<20))
	if mediaType == "text/html" {
		return "", string(body)
	}
	return string(body), ""
}

func walkParts(r io.Reader, boundary string, depth int) (text string, html string) {
	if boundary == "" || depth > 4 {
		return "", ""
	}
	mr := multipart.NewReader(r, boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		mediaType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(mediaType, "multipart/"):
			t, h := walkParts(part, params["boundary"], depth+1)
			if text == "" {
				text = t
			}
			if html == "" {
				html = h
			}
		case mediaType == "text/plain" && text == "":
			body, _ := io.ReadAll(io.LimitReader(part, 4<<20))
			text = string(body)
		case mediaType == "text/html" && html == "":
			body, _ := io.ReadAll(io.LimitReader(part, 4<<20))
			html = string(body)
		}
	}
	return text, html
}

func jsonUnmarshal(raw []byte, out any) error { return json.Unmarshal(raw, out) }
