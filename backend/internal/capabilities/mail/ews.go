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
)

// Mail from an Exchange server that has no IMAP.
//
// Some houses close 993 and 143 and leave only Exchange's own web services on
// 443 — the DHBW is one of them, measured rather than assumed: neither IMAP
// port answers, /EWS/Exchange.asmx answers 401 and asks for NTLM. A mailbox
// nobody can reach is not a mailbox, so this speaks EWS.
//
// It stays inside the promise the rest of the capability makes: what comes back
// is the message as the server has it — MIME, byte for byte — and it is written
// as an .eml like everything else. Nothing here knows about projects or git.
//
// EWS is SOAP, which is verbose and old and entirely predictable. Two calls:
// FindItem says which messages are there, GetItem hands over their MIME. The
// sign-in is NTLM, because that is what the server offers.

const (
	nsSoap     = "http://schemas.xmlsoap.org/soap/envelope/"
	nsTypes    = "http://schemas.microsoft.com/exchange/services/2006/types"
	nsMessages = "http://schemas.microsoft.com/exchange/services/2006/messages"
)

// ewsURL is where the web services live. A person types the address of their
// webmail; the path is always the same one.
func (c config) ewsURL() string {
	host := strings.TrimSpace(c.Host)
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	host = strings.TrimSuffix(host, "/")
	// Somebody will paste the whole OWA address. Keep the machine, drop the rest.
	if i := strings.IndexByte(host, '/'); i > 0 {
		host = host[:i]
	}
	return "https://" + host + "/EWS/Exchange.asmx"
}

// isEWS is the account saying it is an Exchange mailbox rather than an IMAP one.
func (c config) isEWS() bool { return strings.EqualFold(strings.TrimSpace(c.Protocol), "ews") }

// ewsClient carries the sign-in. NTLM needs several round trips over one
// connection, which is what the negotiator does with the transport below.
func ewsClient() *http.Client {
	return &http.Client{
		Transport: ntlmssp.Negotiator{RoundTripper: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			TLSHandshakeTimeout: 20 * time.Second,
		}},
		Timeout: 120 * time.Second,
	}
}

// ewsCall sends one SOAP body and returns the answer. A 401 is reported as what
// it is — the sign-in — because that is the difference between a password to
// enter again and a server to fix.
func ewsCall(ctx context.Context, cfg config, user, secret, body string) ([]byte, error) {
	envelope := `<?xml version="1.0" encoding="utf-8"?>` +
		`<soap:Envelope xmlns:soap="` + nsSoap + `" xmlns:t="` + nsTypes + `" xmlns:m="` + nsMessages + `">` +
		`<soap:Header><t:RequestServerVersion Version="Exchange2013"/></soap:Header>` +
		`<soap:Body>` + body + `</soap:Body></soap:Envelope>`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.ewsURL(),
		strings.NewReader(envelope))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	// The negotiator turns this into NTLM; it is never sent as Basic.
	req.SetBasicAuth(user, secret)

	resp, err := ewsClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s could not be reached: %w", cfg.ewsURL(), err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
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

// ewsFolderID turns a mailbox name into something EWS knows. The named ones are
// the folders every mailbox has; anything else is looked up by its display name.
func ewsFolderID(ctx context.Context, cfg config, user, secret, mailbox string) (string, error) {
	known := map[string]string{
		"": "inbox", "inbox": "inbox", "posteingang": "inbox",
		"sent": "sentitems", "sent items": "sentitems", "gesendet": "sentitems",
		"drafts": "drafts", "entwürfe": "drafts",
		"junk": "junkemail", "spam": "junkemail",
		"trash": "deleteditems", "deleted items": "deleteditems", "papierkorb": "deleteditems",
		"archive": "archive",
	}
	if id, ok := known[strings.ToLower(strings.TrimSpace(mailbox))]; ok {
		return `<t:DistinguishedFolderId Id="` + id + `"/>`, nil
	}

	answer, err := ewsCall(ctx, cfg, user, secret,
		`<m:FindFolder Traversal="Deep">`+
			`<m:FolderShape><t:BaseShape>IdOnly</t:BaseShape>`+
			`<t:AdditionalProperties><t:FieldURI FieldURI="folder:DisplayName"/></t:AdditionalProperties>`+
			`</m:FolderShape>`+
			`<m:ParentFolderIds><t:DistinguishedFolderId Id="msgfolderroot"/></m:ParentFolderIds>`+
			`</m:FindFolder>`)
	if err != nil {
		return "", err
	}
	var doc struct {
		Folders []struct {
			ID          string `xml:"FolderId>Id,attr"`
			DisplayName string `xml:"DisplayName"`
		} `xml:"Body>FindFolderResponse>ResponseMessages>FindFolderResponseMessage>RootFolder>Folders>Folder"`
	}
	if err := xml.Unmarshal(answer, &doc); err != nil {
		return "", fmt.Errorf("the folder list could not be read: %w", err)
	}
	names := make([]string, 0, len(doc.Folders))
	for _, f := range doc.Folders {
		names = append(names, f.DisplayName)
		if strings.EqualFold(f.DisplayName, strings.TrimSpace(mailbox)) {
			return `<t:FolderId Id="` + xmlEscape(f.ID) + `"/>`, nil
		}
	}
	return "", fmt.Errorf("there is no folder called %q — this mailbox has: %s",
		mailbox, strings.Join(names, ", "))
}

// ewsFetchLatest is the newest `count` messages of a folder, as MIME.
func ewsFetchLatest(ctx context.Context, cfg config, user, secret, mailbox string, count int) ([]Message, error) {
	folder, err := ewsFolderID(ctx, cfg, user, secret, mailbox)
	if err != nil {
		return nil, err
	}
	answer, err := ewsCall(ctx, cfg, user, secret,
		`<m:FindItem Traversal="Shallow">`+
			`<m:ItemShape><t:BaseShape>IdOnly</t:BaseShape></m:ItemShape>`+
			fmt.Sprintf(`<m:IndexedPageItemView MaxEntriesReturned="%d" Offset="0" BasePoint="Beginning"/>`, count)+
			`<m:SortOrder><t:FieldOrder Order="Descending">`+
			`<t:FieldURI FieldURI="item:DateTimeReceived"/></t:FieldOrder></m:SortOrder>`+
			`<m:ParentFolderIds>`+folder+`</m:ParentFolderIds>`+
			`</m:FindItem>`)
	if err != nil {
		return nil, err
	}
	var found struct {
		Items []struct {
			ID        string `xml:"ItemId>Id,attr"`
			ChangeKey string `xml:"ItemId>ChangeKey,attr"`
		} `xml:"Body>FindItemResponse>ResponseMessages>FindItemResponseMessage>RootFolder>Items>Message"`
	}
	if err := xml.Unmarshal(answer, &found); err != nil {
		return nil, fmt.Errorf("the message list could not be read: %w", err)
	}
	if len(found.Items) == 0 {
		return nil, nil
	}

	// The MIME comes in a second call, in bites: one request for two hundred
	// messages is a request that times out.
	out := make([]Message, 0, len(found.Items))
	const chunk = 20
	for start := 0; start < len(found.Items); start += chunk {
		end := start + chunk
		if end > len(found.Items) {
			end = len(found.Items)
		}
		var ids bytes.Buffer
		for _, it := range found.Items[start:end] {
			fmt.Fprintf(&ids, `<t:ItemId Id="%s" ChangeKey="%s"/>`,
				xmlEscape(it.ID), xmlEscape(it.ChangeKey))
		}
		body, err := ewsCall(ctx, cfg, user, secret,
			`<m:GetItem><m:ItemShape><t:BaseShape>IdOnly</t:BaseShape>`+
				`<t:IncludeMimeContent>true</t:IncludeMimeContent></m:ItemShape>`+
				`<m:ItemIds>`+ids.String()+`</m:ItemIds></m:GetItem>`)
		if err != nil {
			return nil, err
		}
		var got struct {
			Messages []struct {
				MIME string `xml:"MimeContent"`
				ID   string `xml:"ItemId>Id,attr"`
			} `xml:"Body>GetItemResponse>ResponseMessages>GetItemResponseMessage>Items>Message"`
		}
		if err := xml.Unmarshal(body, &got); err != nil {
			return nil, fmt.Errorf("a message could not be read: %w", err)
		}
		for _, m := range got.Messages {
			raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(m.MIME))
			if derr != nil || len(raw) == 0 {
				continue
			}
			out = append(out, Message{UID: ewsUID(m.ID), Raw: raw})
		}
	}
	return out, nil
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

// ewsPing is the sign-in on its own: one small call that fails loudly if the
// password is wrong. It is the same single attempt as an IMAP login.
func ewsPing(ctx context.Context, cfg config, user, secret string) error {
	_, err := ewsCall(ctx, cfg, user, secret,
		`<m:GetFolder><m:FolderShape><t:BaseShape>IdOnly</t:BaseShape></m:FolderShape>`+
			`<m:FolderIds><t:DistinguishedFolderId Id="inbox"/></m:FolderIds></m:GetFolder>`)
	return err
}

// ewsSend posts a message through Exchange, for the mailboxes where SMTP is
// shut as well. The server puts a copy in Sent Items, as it would for OWA.
func ewsSend(ctx context.Context, cfg config, user, secret, to, subject, body string) error {
	var recipients bytes.Buffer
	for _, address := range splitRecipients(to) {
		fmt.Fprintf(&recipients, `<t:Mailbox><t:EmailAddress>%s</t:EmailAddress></t:Mailbox>`,
			xmlEscape(address))
	}
	_, err := ewsCall(ctx, cfg, user, secret,
		`<m:CreateItem MessageDisposition="SendAndSaveCopy">`+
			`<m:SavedItemFolderId><t:DistinguishedFolderId Id="sentitems"/></m:SavedItemFolderId>`+
			`<m:Items><t:Message>`+
			`<t:Subject>`+xmlEscape(subject)+`</t:Subject>`+
			`<t:Body BodyType="Text">`+xmlEscape(body)+`</t:Body>`+
			`<t:ToRecipients>`+recipients.String()+`</t:ToRecipients>`+
			`</t:Message></m:Items></m:CreateItem>`)
	return err
}

func xmlEscape(s string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}
