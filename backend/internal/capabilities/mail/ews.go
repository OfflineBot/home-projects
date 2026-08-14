package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/go-ntlmssp"
	"github.com/OfflineBot/nicht-libs/email"
)

// Mail from an Exchange server that has no IMAP.
//
// Some houses close 993 and 143 and leave only Exchange's own web services on
// 443 — the DHBW is one of them, measured rather than assumed: neither IMAP
// port answers, /EWS/Exchange.asmx answers 401 and asks for NTLM.
//
// The talking is done by github.com/OfflineBot/nicht-libs/email, which was
// written against this very mailbox and knows what it answers: NTLM, the
// folders, the paging, the throttling when too much is asked at once. Writing
// that a second time here bought nothing and cost a wrong XML tag that failed
// a run after the password had already been spent.
//
// What it does not hand out is the raw MIME, and that is the one thing this
// capability promises: a mail is the .eml the server has, byte for byte. So a
// single call of our own asks for exactly that and nothing else.

// ewsURL is where the web services live. A person types the address of their
// webmail; the path is always the same one.
func (c config) ewsURL() string { return ewsHostOf(c.Host) + "/EWS/Exchange.asmx" }

// ewsHostOf keeps the machine out of whatever was pasted — the bare host, the
// full OWA address, with or without a scheme.
func ewsHostOf(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	host = strings.TrimSuffix(host, "/")
	if i := strings.IndexByte(host, '/'); i > 0 {
		host = host[:i]
	}
	return "https://" + host
}

// isEWS is the account saying it is an Exchange mailbox rather than an IMAP one.
func (c config) isEWS() bool { return strings.EqualFold(strings.TrimSpace(c.Protocol), "ews") }

// ewsPing is the sign-in on its own, and the same single attempt as an IMAP
// login: it either works or the password is gone.
func ewsPing(ctx context.Context, cfg config, user, secret string) error {
	done := make(chan error, 1)
	go func() {
		_, err := email.TestConnection(ewsHostOf(cfg.Host), user, secret)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ewsFetchLatest is the newest `count` messages of a folder, as MIME. A count
// of zero means the whole folder, page by page — a mailbox that has been
// running for three years is not fifty messages.
func ewsFetchLatest(ctx context.Context, cfg config, user, secret, mailbox string, count int,
	log func(string, ...any)) ([]Message, error) {

	host := ewsHostOf(cfg.Host)
	const page = 100

	ids := []string{}
	for offset := 0; ; offset += page {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		want := page
		if count > 0 && count-len(ids) < page {
			want = count - len(ids)
		}
		list, err := email.GetMessages(host, user, secret, mailbox, want, offset)
		if err != nil {
			return nil, err
		}
		for _, m := range list.Emails {
			raw, derr := email.DecodeItemID(m.EWSID)
			if derr != nil || raw == "" {
				continue
			}
			ids = append(ids, raw)
		}
		if offset == 0 && log != nil {
			named := strings.ToLower(strings.TrimSpace(mailbox))
			if named != "" && named != "inbox" && !knownFolder(named) {
				log("Exchange has no folder called %q here — reading the inbox", mailbox)
			}
			log("%s holds %d message(s)", mailbox, list.Total)
		}
		if len(list.Emails) == 0 || (count > 0 && len(ids) >= count) || len(ids) >= list.Total {
			break
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}

	// The MIME comes in a second pass, in bites: one request for two thousand
	// messages is a request the server throttles.
	out := make([]Message, 0, len(ids))
	const chunk = 20
	for start := 0; start < len(ids); start += chunk {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		found, err := ewsMIME(ctx, cfg, user, secret, ids[start:end])
		if err != nil {
			return out, err
		}
		out = append(out, found...)
		if log != nil && len(ids) > chunk {
			log("%d of %d fetched", len(out), len(ids))
		}
	}
	return out, nil
}

// ewsMIME asks for the messages as the server has them. This is the one piece
// the library keeps to itself, so it is asked for here — and for nothing else.
func ewsMIME(ctx context.Context, cfg config, user, secret string, rawIDs []string) ([]Message, error) {
	var ids bytes.Buffer
	for _, id := range rawIDs {
		fmt.Fprintf(&ids, `<t:ItemId Id="%s"/>`, xmlEscape(id))
	}
	body, err := ewsCall(ctx, cfg, user, secret,
		`<m:GetItem><m:ItemShape><t:BaseShape>IdOnly</t:BaseShape>`+
			`<t:IncludeMimeContent>true</t:IncludeMimeContent></m:ItemShape>`+
			`<m:ItemIds>`+ids.String()+`</m:ItemIds></m:GetItem>`)
	if err != nil {
		return nil, err
	}
	return parseMIME(body)
}

// parseMIME is the reading half of ewsMIME, kept on its own so it can be held
// against a real answer in a test without a mailbox.
func parseMIME(body []byte) ([]Message, error) {
	// Note the shape: encoding/xml cannot follow a path into an attribute, so
	// the item id is its own struct. Writing it the short way is what broke a
	// run once — "ItemId>Id chain not valid with attr flag".
	var answer struct {
		Messages []struct {
			MIME   string `xml:"MimeContent"`
			ItemID struct {
				ID string `xml:"Id,attr"`
			} `xml:"ItemId"`
		} `xml:"Body>GetItemResponse>ResponseMessages>GetItemResponseMessage>Items>Message"`
	}
	if err := xml.Unmarshal(body, &answer); err != nil {
		return nil, fmt.Errorf("the messages could not be read: %w", err)
	}
	out := make([]Message, 0, len(answer.Messages))
	for _, m := range answer.Messages {
		raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(m.MIME))
		if derr != nil || len(raw) == 0 {
			continue
		}
		out = append(out, Message{UID: ewsUID(m.ItemID.ID), Raw: raw})
	}
	return out, nil
}

// ewsCall sends one SOAP body and returns the answer.
func ewsCall(ctx context.Context, cfg config, user, secret, body string) ([]byte, error) {
	envelope := `<?xml version="1.0" encoding="utf-8"?>` +
		`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types" ` +
		`xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages">` +
		`<soap:Header><t:RequestServerVersion Version="Exchange2013"/></soap:Header>` +
		`<soap:Body>` + body + `</soap:Body></soap:Envelope>`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.ewsURL(), strings.NewReader(envelope))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	// The negotiator turns this into NTLM; it is never sent as Basic.
	req.SetBasicAuth(user, secret)

	client := &http.Client{
		Transport: ntlmssp.Negotiator{RoundTripper: &http.Transport{
			Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 20 * time.Second,
		}},
		Timeout: 5 * time.Minute,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s could not be reached: %w", cfg.ewsURL(), err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("the server refused the sign-in for %q", user)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%s is not there — is that the right webmail address?", cfg.ewsURL())
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("the server answered %d: %s", resp.StatusCode, firstLine(answer))
	}
	if fault := soapFault(answer); fault != "" {
		return nil, fmt.Errorf("the server said: %s", fault)
	}
	return answer, nil
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// soapFault pulls the human half out of a SOAP fault, which is the only part
// worth passing on.
func soapFault(b []byte) string {
	var doc struct {
		Fault struct {
			String string `xml:"faultstring"`
		} `xml:"Body>Fault"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return ""
	}
	return strings.TrimSpace(doc.Fault.String)
}

// ewsUID is a short, stable name for a message. The item id is a hundred
// characters of base64 and would make an unreadable file name; the tail of it
// is stable for as long as the message stays where it is, which is what a file
// name needs.
func ewsUID(id string) string {
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, id)
	if len(clean) > 12 {
		return clean[len(clean)-12:]
	}
	return clean
}

// ewsSend posts a message through Exchange, for the mailboxes where SMTP is
// shut as well. The server puts a copy in Sent Items, as it would for OWA.
func ewsSend(ctx context.Context, cfg config, user, secret, to, subject, body string) error {
	done := make(chan error, 1)
	go func() {
		done <- email.SendEmail(ewsHostOf(cfg.Host), user, secret,
			splitRecipients(to), nil, nil, subject, body, false)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func xmlEscape(s string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

// knownFolder is what the library can name. Anything else it quietly reads as
// the inbox, which is worth saying out loud rather than discovering later.
func knownFolder(name string) bool {
	switch name {
	case "inbox", "sent", "sentitems", "drafts", "deleted", "deleteditems", "trash",
		"junk", "spam", "junkemail":
		return true
	}
	return false
}
