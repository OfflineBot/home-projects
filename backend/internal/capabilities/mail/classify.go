package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	netmail "net/mail"
	"sort"
	"strings"
	"time"

	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// Sorting mail into categories.
//
// Two ways, and the same result either way. Without anything configured, a
// handful of rules that need no model at all: an invoice says "Rechnung", a
// newsletter says "abmelden", a university writes from a university. They are
// not clever and they are not meant to be — they are what makes the feature
// useful on the first day.
//
// With a classifier configured, the text goes to it and its answer wins. That
// is a service of your own, in a container of your own; this end of it is one
// POST and one JSON object back, so anything that can answer a request can be
// the classifier. Nothing about it is a plug-in: there is one place to put a
// URL, because this changes about once a year.
//
// The answer is kept in labels.json — a file, like everything else, so it
// travels with git and can be corrected by hand.

// LabelFile is where the answers live.
const LabelFile = "labels.json"

// Labels is what a project remembers about its own mail.
type Labels struct {
	// By is what did the labelling: "rules" or the classifier's address.
	By     string           `json:"by,omitempty"`
	At     string           `json:"at,omitempty"`
	Labels map[string]Label `json:"labels"`
}

type Label struct {
	Label string  `json:"label"`
	Score float64 `json:"score,omitempty"`
	// Fixed marks a label a person set. A run leaves those alone: a machine
	// that overwrites a correction every night is worse than no machine.
	Fixed bool `json:"fixed,omitempty"`
}

// Classifier is where the answers come from, kept in classifier.json.
type Classifier struct {
	// Endpoint takes a POST with {subject, from, text} and answers
	// {label, score}. Empty means the rules below.
	Endpoint string `json:"endpoint,omitempty"`
	Token    string `json:"token,omitempty"`
	// Categories is what may come back. Empty means whatever it says.
	Categories []string `json:"categories,omitempty"`
}

// The categories the rules know. A classifier of your own may use these or its
// own — they are a starting point, not a schema.
const (
	CatUniversity = "university"
	CatInvoice    = "invoice"
	CatNewsletter = "newsletter"
	CatDelivery   = "delivery"
	CatSecurity   = "security"
	CatPersonal   = "personal"
	CatOther      = "other"
)

var ruleWords = []struct {
	category string
	words    []string
}{
	{CatSecurity, []string{"passwort", "password", "verifizier", "verify", "anmeldeversuch",
		"sign-in", "sicherheitscode", "two-factor", "bestätigungscode", "code:"}},
	{CatInvoice, []string{"rechnung", "invoice", "zahlung", "payment", "mahnung", "beleg",
		"quittung", "receipt", "lastschrift", "betrag"}},
	{CatDelivery, []string{"sendung", "paket", "lieferung", "versand", "tracking", "zustellung",
		"dhl", "hermes", "shipment"}},
	{CatNewsletter, []string{"newsletter", "abmelden", "unsubscribe", "angebot", "rabatt",
		"% off", "sale", "nicht mehr erhalten"}},
	{CatUniversity, []string{"vorlesung", "klausur", "prüfung", "semester", "moodle", "dhbw",
		"studien", "dozent", "seminar", "hochschule"}},
}

// byRules is the answer without a model: what the words say.
func byRules(m Summary, body string) Label {
	haystack := strings.ToLower(m.Subject + " " + m.From + " " + body)
	if strings.Contains(strings.ToLower(m.From), ".edu") ||
		strings.Contains(strings.ToLower(m.From), "hochschule") ||
		strings.Contains(strings.ToLower(m.From), "dhbw") ||
		strings.Contains(strings.ToLower(m.From), "uni-") {
		return Label{Label: CatUniversity, Score: 0.6}
	}
	best, hits := CatOther, 0
	for _, rule := range ruleWords {
		found := 0
		for _, w := range rule.words {
			if strings.Contains(haystack, w) {
				found++
			}
		}
		if found > hits {
			best, hits = rule.category, found
		}
	}
	if hits == 0 {
		// Nothing matched. A message from a person, most likely — but said as a
		// guess, not as a verdict.
		return Label{Label: CatOther, Score: 0}
	}
	score := float64(hits) / 4
	if score > 0.9 {
		score = 0.9
	}
	return Label{Label: best, Score: score}
}

// byService asks the classifier. Anything that answers a POST with a label can
// be one; a failure is reported rather than silently falling back, because a
// model that is down and a model that says "other" are different situations.
func byService(ctx context.Context, cfg Classifier, m Summary, body string) (Label, error) {
	payload, _ := json.Marshal(map[string]any{
		"subject": m.Subject, "from": m.From, "to": m.To, "text": body,
		"categories": cfg.Categories,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return Label{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return Label{}, fmt.Errorf("the classifier could not be reached: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return Label{}, fmt.Errorf("the classifier answered %d", resp.StatusCode)
	}
	var answer struct {
		Label string  `json:"label"`
		Score float64 `json:"score"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		return Label{}, fmt.Errorf("the classifier's answer could not be read: %w", err)
	}
	if strings.TrimSpace(answer.Label) == "" {
		return Label{}, fmt.Errorf("the classifier returned no label")
	}
	return Label{Label: answer.Label, Score: answer.Score}, nil
}

func readClassifier(ctx context.Context, env *capability.Env, p *model.Project) Classifier {
	var cfg Classifier
	body, err := env.Files.ReadLocal(ctx, p, "classifier.json")
	if err == nil {
		_ = json.Unmarshal(body, &cfg)
	}
	return cfg
}

func readLabels(ctx context.Context, env *capability.Env, p *model.Project) Labels {
	out := Labels{Labels: map[string]Label{}}
	body, err := env.Files.ReadLocal(ctx, p, LabelFile)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(body, &out)
	if out.Labels == nil {
		out.Labels = map[string]Label{}
	}
	return out
}

func writeLabels(ctx context.Context, env *capability.Env, p *model.Project, l Labels, author, email string) error {
	body, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	_, err = env.Files.Write(ctx, p, LabelFile, append(body, '\n'), files.Op{
		Author: author, Email: email, Message: "Sort the mail into categories", Commit: true,
	})
	return err
}

// Classify labels the messages that have no label yet, or all of them when
// asked. A label someone set by hand is never touched.
func Classify(ctx context.Context, env *capability.Env, p *model.Project, all bool,
	author, email string) (Labels, int, error) {

	cfg := readClassifier(ctx, env, p)
	labels := readLabels(ctx, env, p)
	labels.By = "rules"
	if cfg.Endpoint != "" {
		labels.By = cfg.Endpoint
	}
	labels.At = time.Now().Format(time.RFC3339)

	messages := listMessages(env, p)
	sort.Slice(messages, func(i, j int) bool { return messages[i].Path < messages[j].Path })

	done := 0
	for _, m := range messages {
		if existing, ok := labels.Labels[m.Path]; ok && (existing.Fixed || !all) {
			continue
		}
		body := ""
		if raw, err := env.Files.ReadLocal(ctx, p, m.Path); err == nil {
			if parsed, perr := netmail.ReadMessage(bytes.NewReader(raw)); perr == nil {
				text, html := extractBodies(parsed)
				body = text
				if body == "" {
					body = html
				}
			}
		}
		if len(body) > 4000 {
			body = body[:4000] // the first page decides; the rest is signature
		}
		var label Label
		if cfg.Endpoint != "" {
			answer, err := byService(ctx, cfg, m, body)
			if err != nil {
				return labels, done, err
			}
			label = answer
		} else {
			label = byRules(m, body)
		}
		labels.Labels[m.Path] = label
		done++
	}

	// A label for a message that is gone is not worth keeping.
	present := map[string]bool{}
	for _, m := range messages {
		present[m.Path] = true
	}
	for path := range labels.Labels {
		if !present[path] {
			delete(labels.Labels, path)
		}
	}

	if err := writeLabels(ctx, env, p, labels, author, email); err != nil {
		return labels, done, err
	}
	return labels, done, nil
}
