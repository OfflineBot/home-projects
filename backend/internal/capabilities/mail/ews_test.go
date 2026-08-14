package mail

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Reading what Exchange actually answers.
//
// This test exists because of one failed run: the item id was written as
// `xml:"ItemId>Id,attr"`, which encoding/xml refuses at run time and not at
// compile time — "ItemId>Id chain not valid with attr flag". The password had
// already been spent by then, so the cost of that mistake was a trip to the
// webmail to type it in again.
//
// The answer below is shaped like the real one: two messages, the namespaces
// Exchange uses, the MIME in base64.
func TestTheAnswerFromExchangeIsRead(t *testing.T) {
	mime := "From: someone@example.com\r\nSubject: hello\r\n\r\nthe body\r\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(mime))
	answer := `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
 <s:Body>
  <m:GetItemResponse xmlns:m="http://schemas.microsoft.com/exchange/services/2006/messages"
                     xmlns:t="http://schemas.microsoft.com/exchange/services/2006/types">
   <m:ResponseMessages>
    <m:GetItemResponseMessage ResponseClass="Success">
     <m:ResponseCode>NoError</m:ResponseCode>
     <m:Items>
      <t:Message>
       <t:MimeContent CharacterSet="UTF-8">` + encoded + `</t:MimeContent>
       <t:ItemId Id="AAMkAGI1NzQyZTVmLLONGTAIL0001" ChangeKey="CQAAABYAAAA"/>
       <t:Subject>hello</t:Subject>
      </t:Message>
     </m:Items>
    </m:GetItemResponseMessage>
    <m:GetItemResponseMessage ResponseClass="Success">
     <m:ResponseCode>NoError</m:ResponseCode>
     <m:Items>
      <t:Message>
       <t:MimeContent CharacterSet="UTF-8">` + encoded + `</t:MimeContent>
       <t:ItemId Id="AAMkAGI1NzQyZTVmLLONGTAIL0002" ChangeKey="CQAAABYAAAB"/>
      </t:Message>
     </m:Items>
    </m:GetItemResponseMessage>
   </m:ResponseMessages>
  </m:GetItemResponse>
 </s:Body>
</s:Envelope>`

	messages, err := parseMIME([]byte(answer))
	if err != nil {
		t.Fatalf("the answer could not be read: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("wanted 2 messages, got %d", len(messages))
	}
	if string(messages[0].Raw) != mime {
		t.Errorf("the message is not what the server had:\n%q", messages[0].Raw)
	}
	// Two messages must not end up as one file.
	if messages[0].UID == messages[1].UID {
		t.Errorf("both messages got the same name %q", messages[0].UID)
	}
	if !strings.HasSuffix("AAMkAGI1NzQyZTVmLLONGTAIL0001", messages[0].UID) {
		t.Errorf("the name %q does not come from the item id", messages[0].UID)
	}
}

// A fault comes back as the sentence the server wrote, not as an empty list —
// otherwise a run would report "0 new" and look like a success.
func TestAFaultIsNotAnEmptyMailbox(t *testing.T) {
	fault := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
 <s:Body><s:Fault>
  <faultcode>s:Server</faultcode>
  <faultstring>The mailbox database is temporarily unavailable.</faultstring>
 </s:Fault></s:Body>
</s:Envelope>`
	if got := soapFault([]byte(fault)); !strings.Contains(got, "temporarily unavailable") {
		t.Errorf("the server's own words were lost: %q", got)
	}
	if got := soapFault([]byte(`<s:Envelope xmlns:s="x"><s:Body><ok/></s:Body></s:Envelope>`)); got != "" {
		t.Errorf("a good answer was read as a fault: %q", got)
	}
}

// Whatever gets pasted into the address field, the web services are at one place.
func TestTheWebmailAddressIsUnderstood(t *testing.T) {
	for _, in := range []string{
		"webmail.dhbw-ravensburg.de",
		"https://webmail.dhbw-ravensburg.de",
		"https://webmail.dhbw-ravensburg.de/owa/auth/logon.aspx?url=https%3a%2f%2fwebmail",
		" webmail.dhbw-ravensburg.de/ ",
	} {
		cfg := config{Host: in}
		if got := cfg.ewsURL(); got != "https://webmail.dhbw-ravensburg.de/EWS/Exchange.asmx" {
			t.Errorf("%q became %q", in, got)
		}
	}
}
