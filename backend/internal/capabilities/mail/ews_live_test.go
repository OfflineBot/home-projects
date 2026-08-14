package mail

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Against a real Exchange server, when one is named.
//
// EWS cannot be usefully faked: the parts that break are the SOAP the server
// accepts and the sign-in it demands, and a stub would agree with whatever this
// code happens to send. So this talks to a real mailbox when the environment
// names one, and says clearly that it did nothing when it does not.
//
//	HP_EWS_HOST=webmail.example.de HP_EWS_USER=… HP_EWS_PASSWORD=… go test ./internal/capabilities/mail -run EWS -v
//
// Note the cost of a wrong password on such a server: failed sign-ins count
// against the account, and enough of them lock it for everything else too. That
// is why the fetch runs only after the sign-in has already been shown to work.
func TestEWSAgainstARealServer(t *testing.T) {
	host, user, password := os.Getenv("HP_EWS_HOST"), os.Getenv("HP_EWS_USER"), os.Getenv("HP_EWS_PASSWORD")
	if host == "" || user == "" || password == "" {
		t.Skip("HP_EWS_HOST/USER/PASSWORD are not set — nothing was measured")
	}
	cfg := config{Protocol: "ews", Host: host, User: user}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := ewsPing(ctx, cfg, user, password); err != nil {
		t.Fatalf("sign-in: %v", err)
	}

	messages, err := ewsFetchLatest(ctx, cfg, user, password, "INBOX", 3, t.Logf)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(messages) == 0 {
		t.Log("the inbox is empty — the sign-in and the request worked")
		return
	}
	for _, m := range messages {
		if m.UID == "" {
			t.Error("a message came back without a name to file it under")
		}
		// What is written to the project is what the server has, so it has to
		// be a mail rather than a fragment of XML.
		head := string(m.Raw[:min(len(m.Raw), 400)])
		if !strings.Contains(strings.ToLower(head), "from:") &&
			!strings.Contains(strings.ToLower(head), "received:") {
			t.Errorf("this does not look like MIME: %.120q", head)
		}
	}
	t.Logf("%d message(s), the newest %d bytes", len(messages), len(messages[0].Raw))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
