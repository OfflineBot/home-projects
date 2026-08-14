// Package mail is the mailbox capability: read, classify and send.
//
// A mail is an `.eml` file in the project — RFC 822, exactly what the server
// delivered. That is why it can be downloaded, versioned and opened in any
// other mail program.
package mail

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/slug"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

// Inbox is where a fetch puts what it finds.
const Inbox = "inbox"

type Capability struct{ capability.Base }

func New() Capability { return Capability{} }

func (Capability) Name() string   { return "mail" }
func (Capability) Title() string  { return "Mail" }
func (Capability) Icon() string   { return "mail" }
func (Capability) Owns() []string { return []string{"*.eml", "labels.json", "classifier.json"} }

func (Capability) Presets() []capability.Preset {
	return []capability.Preset{{
		Key:          "mail",
		Title:        "Mail",
		Description:  "A mailbox: messages as .eml files, read and send.",
		Icon:         "mail",
		DefaultTab:   "mail",
		Capabilities: []string{"mail"},
		Seed: []capability.SeedFile{{
			Path: Inbox + "/.keep",
			Content: func(p *model.Project) []byte {
				return []byte("Messages fetched by a mail scheduler land in this folder.\n")
			},
		}},
	}}
}

func (Capability) AccountKinds() []capability.AccountKind {
	return []capability.AccountKind{{
		Name:        "mail",
		Title:       "Mail (IMAP/SMTP)",
		Description: "Fetches with IMAP and sends with SMTP. The password is single-use: a failed sign-in deletes it, because mail servers lock accounts too.",
		Fields: []capability.AccountField{
			{Name: "protocol", Label: "How", Type: "select", Default: "imap",
				Options: []capability.Option{
					{Value: "imap", Label: "IMAP"},
					{Value: "ews", Label: "Exchange (EWS)"},
				},
				Hint: "Exchange is for the servers that have no IMAP — a university or a company."},
			{Name: "host", Label: "IMAP server", Type: "text", Required: true, Placeholder: "imap.example.com",
				Hint: "With Exchange, the address of the webmail: webmail.example.de"},
			{Name: "port", Label: "IMAP port", Type: "number", Placeholder: "993"},
			{Name: "user", Label: "User", Type: "text", Required: true},
			{Name: "from", Label: "Sender address", Type: "text"},
			{Name: "smtpHost", Label: "SMTP server", Type: "text", Placeholder: "smtp.example.com"},
			{Name: "smtpPort", Label: "SMTP port", Type: "number", Placeholder: "587"},
		},
		Providers: []capability.Provider{
			{Name: "gmail", Title: "Gmail", Fields: map[string]string{
				"protocol": "imap", "host": "imap.gmail.com", "port": "993",
				"smtpHost": "smtp.gmail.com", "smtpPort": "587",
			}, Note: "Gmail refuses your normal password: make an app password with two-factor on."},
			{Name: "outlook", Title: "Outlook / Microsoft 365", Fields: map[string]string{
				"protocol": "imap", "host": "outlook.office365.com", "port": "993",
				"smtpHost": "smtp.office365.com", "smtpPort": "587",
			}, Note: "Works where the organisation still allows IMAP; many switch it off."},
			{Name: "gmx", Title: "GMX", Fields: map[string]string{
				"protocol": "imap", "host": "imap.gmx.net", "port": "993",
				"smtpHost": "mail.gmx.net", "smtpPort": "587",
			}, Note: "IMAP has to be switched on once in the GMX settings."},
			{Name: "webde", Title: "WEB.DE", Fields: map[string]string{
				"protocol": "imap", "host": "imap.web.de", "port": "993",
				"smtpHost": "smtp.web.de", "smtpPort": "587",
			}, Note: "IMAP has to be switched on once in the WEB.DE settings."},
			{Name: "posteo", Title: "Posteo", Fields: map[string]string{
				"protocol": "imap", "host": "posteo.de", "port": "993",
				"smtpHost": "posteo.de", "smtpPort": "587",
			}},
			{Name: "mailbox", Title: "mailbox.org", Fields: map[string]string{
				"protocol": "imap", "host": "imap.mailbox.org", "port": "993",
				"smtpHost": "smtp.mailbox.org", "smtpPort": "587",
			}},
			{Name: "icloud", Title: "iCloud", Fields: map[string]string{
				"protocol": "imap", "host": "imap.mail.me.com", "port": "993",
				"smtpHost": "smtp.mail.me.com", "smtpPort": "587",
			}, Note: "Needs an app-specific password from appleid.apple.com."},
			{Name: "dhbw", Title: "DHBW Ravensburg", Fields: map[string]string{
				"protocol": "ews", "host": "webmail.dhbw-ravensburg.de",
			}, Note: "That server has no IMAP — it is fetched and sent over Exchange. " +
				"The user name is the one from Moodle, without a domain in front."},
		},
		SecretLabel: "Password",
		Locks:       true,
		Precheck:    precheckMail,
		Test:        testMailAccount,
	}}
}

func (Capability) SchedulerKinds() []capability.SchedulerKind {
	return []capability.SchedulerKind{{
		Name:            "mail",
		Title:           "Fetch mail",
		Description:     "Fetches the newest messages into the project as .eml files.",
		AccountKinds:    []string{"mail"},
		AccountRequired: true,
		Options: []capability.AccountField{
			{Name: "mailbox", Label: "Which folder", Type: "text", Placeholder: "INBOX"},
			{Name: "count", Label: "How many of the newest", Type: "number", Placeholder: "all of them",
				Hint: "Empty fetches the whole folder. A number keeps it to that many."},
		},
		Run: runFetch,
	}}
}

func (Capability) Actions() []capability.Action {
	return []capability.Action{{
		Name:        "sendmail",
		Title:       "Send mail",
		Description: "Sends a mail through a mail account.",
		Params:      []string{"account", "to", "subject", "body"},
		Run:         runSendAction,
	}}
}

// ---------------------------------------------------------------- fetching

type config struct {
	// Protocol is "imap" or "ews". Empty means IMAP, which is what nearly every
	// mailbox speaks; "ews" is for the Exchange servers that have no IMAP at all.
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     any    `json:"port"`
	User     string `json:"user"`
	From     string `json:"from"`
	SMTPHost string `json:"smtpHost"`
	SMTPPort any    `json:"smtpPort"`
}

func (c config) imapPort() int {
	if n := toInt(c.Port); n > 0 {
		return n
	}
	return 993
}

func (c config) smtpPort() int {
	if n := toInt(c.SMTPPort); n > 0 {
		return n
	}
	return 587
}

func toInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}

func readConfig(a *model.Account) (config, error) {
	var cfg config
	if err := jsonUnmarshal(a.Config, &cfg); err != nil {
		return cfg, fmt.Errorf("the account's settings cannot be read: %w", err)
	}
	if cfg.Host == "" {
		if cfg.isEWS() {
			return cfg, fmt.Errorf("the account has no webmail address")
		}
		return cfg, fmt.Errorf("the account has no IMAP server")
	}
	if cfg.User == "" {
		return cfg, fmt.Errorf("the account has no user name")
	}
	return cfg, nil
}

// precheckMail answers what can be answered without the password: whether
// there is anything at that address listening at all. A mail server that never
// answers would otherwise eat the password on every attempt — and on a
// university's Exchange, repeated attempts lock the account for everything
// else too.
func precheckMail(ctx context.Context, env *capability.Env, a *model.Account) error {
	cfg, err := readConfig(a)
	if err != nil {
		return err
	}
	if cfg.isEWS() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.ewsURL(), nil)
		if err != nil {
			return err
		}
		resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			return fmt.Errorf("%s does not answer", cfg.ewsURL())
		}
		defer resp.Body.Close()
		// 401 is the healthy answer here: the service is there and wants a
		// sign-in, which is the next step and not this one.
		if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode >= 300 {
			return fmt.Errorf("%s answered %d instead of asking for a sign-in",
				cfg.ewsURL(), resp.StatusCode)
		}
		return nil
	}
	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.imapPort()))
	conn, err := net.DialTimeout("tcp", address, 12*time.Second)
	if err != nil {
		// Before guessing, ask the same machine whether it speaks Exchange. If
		// it does, the answer is not "try something" but "switch this one
		// setting", which is a different sentence.
		if speaksEWS(ctx, cfg) {
			return fmt.Errorf("%s does not answer, but %s does — set \"How\" to Exchange (EWS)",
				address, cfg.ewsURL())
		}
		return fmt.Errorf("%s does not answer — if this is a university or company mailbox, "+
			"it may have no IMAP at all; try Exchange (EWS) instead", address)
	}
	conn.Close()
	return nil
}

// speaksEWS is the same host asked over HTTPS. A 401 means the service is
// there and wants a sign-in, which is exactly what we are looking for.
func speaksEWS(ctx context.Context, cfg config) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.ewsURL(), nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusOK
}

func testMailAccount(ctx context.Context, env *capability.Env, a *model.Account, secret []byte) error {
	cfg, err := readConfig(a)
	if err != nil {
		return err
	}
	if cfg.isEWS() {
		return ewsPing(ctx, cfg, cfg.User, string(secret))
	}
	client, err := dialIMAP(cfg.Host, cfg.imapPort(), true, 30*time.Second)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Login(cfg.User, string(secret)); err != nil {
		return err
	}
	client.Logout()
	return nil
}

func runFetch(ctx context.Context, env *capability.Env, job capability.Job) (capability.Report, error) {
	if job.Account == nil || len(job.Secret) == 0 {
		return capability.Report{}, fmt.Errorf("this scheduler needs a mail account with a password")
	}
	cfg, err := readConfig(job.Account)
	if err != nil {
		return capability.Report{}, err
	}
	mailbox, _ := job.Options["mailbox"].(string)
	if mailbox == "" {
		mailbox = "INBOX"
	}
	// Empty means the whole folder. Fifty was a number nobody chose; a mailbox
	// that has been running for years is not fifty messages.
	count := toInt(job.Options["count"])

	var messages []Message
	if cfg.isEWS() {
		// Exchange without IMAP: the same single attempt, over its own web
		// services. Everything after this point is identical.
		job.Log("connecting to %s", cfg.ewsURL())
		found, err := ewsFetchLatest(ctx, cfg, cfg.User, string(job.Secret), mailbox, count, job.Log)
		if err != nil {
			return capability.Report{}, err
		}
		job.Log("signed in as %s", cfg.User)
		messages = found
	} else {
		job.Log("connecting to %s:%d", cfg.Host, cfg.imapPort())
		client, err := dialIMAP(cfg.Host, cfg.imapPort(), true, 30*time.Second)
		if err != nil {
			return capability.Report{}, err
		}
		defer client.Close()

		// This is the one attempt. Anything other than a tagged OK counts as used.
		if err := client.Login(cfg.User, string(job.Secret)); err != nil {
			return capability.Report{}, err
		}
		job.Log("signed in as %s", cfg.User)

		exists, err := client.Select(mailbox)
		if err != nil {
			return capability.Report{Authenticated: true}, err
		}
		if count <= 0 || count > exists {
			count = exists
		}
		job.Log("%s holds %d message(s)", mailbox, exists)
		messages, err = client.FetchLatest(exists, count)
		if err != nil {
			return capability.Report{Authenticated: true}, err
		}
		client.Logout()
	}

	target := job.Scheduler.TargetPath
	if target == "" {
		target = Inbox
	}
	written := 0
	for _, m := range messages {
		name := messageFileName(m)
		rel := path.Join(target, name)
		if env.Files.Exists(job.Project, rel) {
			continue // already here; a fetch does not duplicate
		}
		if _, err := env.Files.Write(ctx, job.Project, rel, m.Raw, files.Op{
			Author: "the mail scheduler", Email: "scheduler@home-projects",
			Message: "Fetch mail", Commit: false,
		}); err != nil {
			return capability.Report{Authenticated: true}, err
		}
		written++
	}
	if written > 0 && job.Project.GitTracked {
		_, _, _ = env.Files.Commit(ctx, job.Project,
			fmt.Sprintf("Fetch %d message(s)", written), "the mail scheduler", "scheduler@home-projects")
	}

	all := listMessages(env, job.Project)
	return capability.Report{
		Message:       fmt.Sprintf("%d new of %d fetched", written, len(messages)),
		FilesChanged:  written,
		Authenticated: true,
		Variables: []store.VariableInput{
			{Name: "messages", Type: "number", Value: len(all), Source: "capability:mail"},
			{Name: "fetched_at", Type: "date", Value: time.Now(), Source: "capability:mail"},
		},
	}, nil
}

func messageFileName(m Message) string {
	parsed, err := mail.ReadMessage(strings.NewReader(string(m.Raw)))
	stamp := time.Now().Format("20060102-150405")
	subject := "message"
	if err == nil {
		if t, terr := parsed.Header.Date(); terr == nil {
			stamp = t.Format("20060102-150405")
		}
		if s := decodeHeader(parsed.Header.Get("Subject")); s != "" {
			subject = s
		}
	}
	name := stamp + "-" + slug.Make(subject)
	if m.UID != "" {
		name += "-" + m.UID
	}
	if len(name) > 120 {
		name = name[:120]
	}
	return name + ".eml"
}

// ----------------------------------------------------------------- reading

type Summary struct {
	Path      string    `json:"path"`
	Folder    string    `json:"folder"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Subject   string    `json:"subject"`
	Date      time.Time `json:"date"`
	Size      int64     `json:"size"`
	HasAttach bool      `json:"hasAttachments"`
	// Category is what the sorting made of it, filled in when the list is read.
	Category string  `json:"category,omitempty"`
	Score    float64 `json:"score,omitempty"`
	Fixed    bool    `json:"fixed,omitempty"`
}

func listMessages(env *capability.Env, p *model.Project) []Summary {
	fsys := env.Files.Workspace().Open(p.ID)
	out := []Summary{}
	_ = fsys.Walk("", func(e workspace.Entry) error {
		if e.IsDir || !strings.HasSuffix(strings.ToLower(e.Name), ".eml") {
			return nil
		}
		s := Summary{Path: e.Path, Folder: path.Dir(e.Path), Size: e.Size, Date: e.ModifiedAt}
		if body, err := env.Files.ReadLocal(context.Background(), p, e.Path); err == nil {
			if parsed, perr := mail.ReadMessage(strings.NewReader(string(body))); perr == nil {
				s.From = decodeHeader(parsed.Header.Get("From"))
				s.To = decodeHeader(parsed.Header.Get("To"))
				s.Subject = decodeHeader(parsed.Header.Get("Subject"))
				if t, terr := parsed.Header.Date(); terr == nil {
					s.Date = t
				}
				s.HasAttach = strings.Contains(parsed.Header.Get("Content-Type"), "multipart/mixed")
			}
		}
		out = append(out, s)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	return out
}

// Routes are mounted under /api/projects/:project/mail
func (Capability) Routes(env *capability.Env, r fiber.Router) {
	r.Get("/messages", func(c *fiber.Ctx) error {
		if err := capability.RequireRead(c); err != nil {
			return err
		}
		p := capability.Project(c)
		list := listMessages(env, p)
		labels := readLabels(c.UserContext(), env, p)
		for i := range list {
			if l, ok := labels.Labels[list[i].Path]; ok {
				list[i].Category, list[i].Score, list[i].Fixed = l.Label, l.Score, l.Fixed
			}
		}
		return c.JSON(fiber.Map{
			"messages":   list,
			"classifier": readClassifier(c.UserContext(), env, p),
			"sortedBy":   labels.By,
			"sortedAt":   labels.At,
		})
	})

	// Sort the mail into categories: the classifier if one is configured, and
	// otherwise the rules that need nothing at all.
	r.Post("/classify", func(c *fiber.Ctx) error {
		if err := capability.RequireWrite(c); err != nil {
			return err
		}
		p := capability.Project(c)
		author, email := capability.AuthorOf(c)
		labels, done, err := Classify(c.UserContext(), env, p, c.QueryBool("all", false), author, email)
		if err != nil {
			return httpx.New(502, "classifier_failed", "%v", err)
		}
		return c.JSON(fiber.Map{"sorted": done, "by": labels.By})
	})

	// A correction stays a correction: a run never writes over it.
	r.Post("/label", func(c *fiber.Ctx) error {
		if err := capability.RequireWrite(c); err != nil {
			return err
		}
		p := capability.Project(c)
		var in struct {
			Path  string `json:"path"`
			Label string `json:"label"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The change could not be read.")
		}
		labels := readLabels(c.UserContext(), env, p)
		if strings.TrimSpace(in.Label) == "" {
			delete(labels.Labels, in.Path)
		} else {
			labels.Labels[in.Path] = Label{Label: in.Label, Score: 1, Fixed: true}
		}
		author, email := capability.AuthorOf(c)
		if err := writeLabels(c.UserContext(), env, p, labels, author, email); err != nil {
			return httpx.Internal("the label could not be stored").WithCause(err)
		}
		return httpx.OK(c)
	})

	// Where the answers come from.
	r.Put("/classifier", func(c *fiber.Ctx) error {
		if err := capability.RequireWrite(c); err != nil {
			return err
		}
		p := capability.Project(c)
		var cfg Classifier
		if err := c.BodyParser(&cfg); err != nil {
			return httpx.BadRequest("The classifier could not be read.")
		}
		body, _ := json.MarshalIndent(cfg, "", "  ")
		author, email := capability.AuthorOf(c)
		if _, err := env.Files.Write(c.UserContext(), p, "classifier.json", append(body, 10), files.Op{
			Author: author, Email: email, Message: "Point the mail sorting somewhere", Commit: true,
		}); err != nil {
			return httpx.Internal("the classifier could not be stored").WithCause(err)
		}
		return c.JSON(cfg)
	})

	r.Get("/message", func(c *fiber.Ctx) error {
		if err := capability.RequireRead(c); err != nil {
			return err
		}
		p := capability.Project(c)
		rel := c.Query("path")
		body, _, err := env.Files.Read(c.UserContext(), p, rel)
		if err != nil {
			return err
		}
		parsed, perr := mail.ReadMessage(strings.NewReader(string(body)))
		if perr != nil {
			return c.JSON(fiber.Map{"path": rel, "raw": string(body),
				"error": "This .eml could not be parsed: " + perr.Error()})
		}
		text, html := extractBodies(parsed)
		date, _ := parsed.Header.Date()
		return c.JSON(fiber.Map{
			"path":    rel,
			"from":    decodeHeader(parsed.Header.Get("From")),
			"to":      decodeHeader(parsed.Header.Get("To")),
			"cc":      decodeHeader(parsed.Header.Get("Cc")),
			"subject": decodeHeader(parsed.Header.Get("Subject")),
			"date":    date,
			"text":    text,
			"html":    html,
		})
	})

	// Classifying is moving a message into another folder — the folders are
	// the classification, and they are visible in the file tree.
	r.Post("/classify", func(c *fiber.Ctx) error {
		if err := capability.RequireWrite(c); err != nil {
			return err
		}
		p := capability.Project(c)
		var in struct {
			Path   string `json:"path"`
			Folder string `json:"folder"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The message could not be read.")
		}
		if in.Folder == "" {
			return httpx.BadRequest("Name the folder to move it into.")
		}
		target := path.Join(in.Folder, path.Base(in.Path))
		author, email := capability.AuthorOf(c)
		if err := env.Files.Move(c.UserContext(), p, in.Path, target, files.Op{
			Author: author, Email: email, Message: "Move mail to " + in.Folder, Commit: true,
		}); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"path": target})
	})

	r.Post("/send", func(c *fiber.Ctx) error {
		if err := capability.RequireWrite(c); err != nil {
			return err
		}
		p := capability.Project(c)
		var in struct {
			Account string `json:"account"`
			To      string `json:"to"`
			Subject string `json:"subject"`
			Body    string `json:"body"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The message could not be read.")
		}
		id, err := uuid.Parse(in.Account)
		if err != nil {
			return httpx.BadRequest("Pick a mail account to send through.")
		}
		if err := send(c.UserContext(), env, id, in.To, in.Subject, in.Body); err != nil {
			return err
		}
		// A sent message is kept like any other: as a file.
		raw := buildMessage("", in.To, in.Subject, in.Body)
		rel := path.Join("sent", time.Now().Format("20060102-150405")+"-"+slug.Make(in.Subject)+".eml")
		author, email := capability.AuthorOf(c)
		if _, err := env.Files.Write(c.UserContext(), p, rel, []byte(raw), files.Op{
			Author: author, Email: email, Message: "Send mail", Commit: true,
		}); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"ok": true, "path": rel})
	})
}

func (Capability) Exports(ctx context.Context, env *capability.Env, p *model.Project) ([]store.VariableInput, error) {
	all := listMessages(env, p)
	inbox := 0
	var newest time.Time
	for _, m := range all {
		if strings.HasPrefix(m.Path, Inbox+"/") {
			inbox++
		}
		if m.Date.After(newest) {
			newest = m.Date
		}
	}
	out := []store.VariableInput{
		{Name: "messages", Type: "number", Value: len(all), Source: "capability:mail"},
		{Name: "inbox", Type: "number", Value: inbox, Source: "capability:mail"},
	}
	if !newest.IsZero() {
		out = append(out, store.VariableInput{
			Name: "latest", Type: "date", Value: newest, Source: "capability:mail",
		})
	}
	return out, nil
}

// ----------------------------------------------------------------- sending

func send(ctx context.Context, env *capability.Env, accountID uuid.UUID, to, subject, body string) error {
	account, err := env.Store.AccountByID(ctx, accountID)
	if err != nil {
		return httpx.NotFound("There is no such account.")
	}
	cfg, err := readConfig(account)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	if cfg.SMTPHost == "" && !cfg.isEWS() {
		return httpx.BadRequest("This account has no SMTP server, so nothing can be sent through it.")
	}
	if to == "" {
		return httpx.BadRequest("A mail needs a recipient.")
	}
	if cfg.isEWS() && cfg.SMTPHost == "" {
		return env.UseAccount(ctx, accountID, func(secret []byte) error {
			return ewsSend(ctx, cfg, cfg.User, string(secret), to, subject, body)
		})
	}
	from := cfg.From
	if from == "" {
		from = cfg.User
	}
	message := buildMessage(from, to, subject, body)

	return env.UseAccount(ctx, accountID, func(secret []byte) error {
		addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.smtpPort()))
		auth := smtp.PlainAuth("", cfg.User, string(secret), cfg.SMTPHost)

		if cfg.smtpPort() == 465 {
			conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12})
			if err != nil {
				return fmt.Errorf("the SMTP server is not reachable: %w", err)
			}
			client, err := smtp.NewClient(conn, cfg.SMTPHost)
			if err != nil {
				return err
			}
			defer client.Quit()
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("sign-in failed: %w", err)
			}
			return sendThrough(client, from, to, message)
		}
		return smtp.SendMail(addr, auth, from, splitRecipients(to), []byte(message))
	})
}

func sendThrough(client *smtp.Client, from, to, message string) error {
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range splitRecipients(to) {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(message)); err != nil {
		return err
	}
	return w.Close()
}

func splitRecipients(to string) []string {
	parts := strings.FieldsFunc(to, func(r rune) bool { return r == ',' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func buildMessage(from, to, subject, body string) string {
	var b strings.Builder
	if from != "" {
		b.WriteString("From: " + from + "\r\n")
	}
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + encodeHeader(subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	b.WriteString("\r\n")
	return b.String()
}

func runSendAction(ctx context.Context, env *capability.Env, in capability.ActionInput) (capability.ActionResult, error) {
	ref := paramOf(in, "account")
	id, err := uuid.Parse(ref)
	if err != nil {
		return capability.ActionResult{}, fmt.Errorf("this action needs the id of a mail account")
	}
	to := paramOf(in, "to")
	subject := paramOf(in, "subject")
	body := paramOf(in, "body")
	if body == "" && in.Previous != nil {
		body = in.Previous.Output
	}
	if err := send(ctx, env, id, to, subject, body); err != nil {
		return capability.ActionResult{}, err
	}
	in.Log("mail sent to %s", to)
	return capability.ActionResult{Output: "mail sent to " + to}, nil
}

func paramOf(in capability.ActionInput, name string) string {
	v, ok := in.Params[name]
	if !ok {
		return ""
	}
	s := fmt.Sprint(v)
	if in.Previous != nil {
		s = strings.ReplaceAll(s, "{{previous}}", in.Previous.Output)
	}
	return s
}
