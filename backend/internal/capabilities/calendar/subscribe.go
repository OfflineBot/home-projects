package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/offlinebot/home-projects/backend/internal/capabilities/calendar/ics"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/slug"
)

// runICSSubscription pulls a foreign calendar (the timetable, a shared
// calendar) and keeps it in the project under subscriptions/. Those events are
// read-only and get overwritten on the next run — the UI says so.
func runICSSubscription(ctx context.Context, env *capability.Env, job capability.Job) (capability.Report, error) {
	url, _ := job.Options["url"].(string)
	if url == "" && job.Account != nil {
		var cfg struct {
			URL string `json:"url"`
		}
		_ = jsonUnmarshal(job.Account.Config, &cfg)
		url = cfg.URL
	}
	if url == "" {
		return capability.Report{}, fmt.Errorf("this subscription has no URL — set one on the scheduler or its account")
	}

	name, _ := job.Options["name"].(string)
	if name == "" {
		name = slug.Make(job.Scheduler.Title)
	}
	if name == "" {
		name = "subscription"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return capability.Report{}, fmt.Errorf("the URL cannot be used: %w", err)
	}
	req.Header.Set("Accept", "text/calendar, text/plain;q=0.5")
	req.Header.Set("User-Agent", "home-projects/1.0")

	// Basic auth, if the account carries one. The password was reserved before
	// this call, so any outcome other than a clean 2xx counts as used up.
	usedCredential := false
	if job.Account != nil && len(job.Secret) > 0 {
		var cfg struct {
			User string `json:"user"`
		}
		_ = jsonUnmarshal(job.Account.Config, &cfg)
		if cfg.User != "" {
			req.SetBasicAuth(cfg.User, string(job.Secret))
			usedCredential = true
		}
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return capability.Report{}, fmt.Errorf("the calendar could not be fetched: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return capability.Report{}, fmt.Errorf("the server refused the credentials (%s)", resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return capability.Report{}, fmt.Errorf("the server answered %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return capability.Report{}, fmt.Errorf("the answer could not be read: %w", err)
	}
	cal, err := ics.ParseCalendar(body)
	if err != nil {
		return capability.Report{}, fmt.Errorf("what came back is not a calendar: %w", err)
	}
	events := cal.Kids("VEVENT")
	job.Log("fetched %d bytes, %d events", len(body), len(events))

	rel := path.Join(SubscriptionDir, slug.Make(name)+".ics")
	target := ics.NewCalendar(job.Project.Title + " · " + name)
	target.Children = append(target.Children, cal.Children...)

	m := lockFor(job.Project.ID)
	m.Lock()
	_, err = env.Files.Write(ctx, job.Project, rel, target.Bytes(), filesOp{
		Author: "the subscription", Email: "scheduler@home-projects",
		Message: "Update subscription " + name, Commit: true,
	})
	m.Unlock()
	if err != nil {
		return capability.Report{}, err
	}
	if err := Reindex(ctx, env, job.Project); err != nil {
		return capability.Report{}, err
	}

	return capability.Report{
		Message:       fmt.Sprintf("%d events from %s", len(events), hostOf(url)),
		FilesChanged:  1,
		Authenticated: usedCredential,
	}, nil
}

func hostOf(url string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	if i := strings.IndexAny(s, "/?"); i > 0 {
		return s[:i]
	}
	return s
}

// jsonUnmarshal keeps the small amount of JSON handling this file needs in one
// place.
func jsonUnmarshal(raw []byte, out any) error { return json.Unmarshal(raw, out) }

// testICSAccount fetches the URL once, with the credentials if there are any.
func testICSAccount(ctx context.Context, env *capability.Env, a *model.Account, secret []byte) error {
	var cfg struct {
		URL  string `json:"url"`
		User string `json:"user"`
	}
	if err := jsonUnmarshal(a.Config, &cfg); err != nil || cfg.URL == "" {
		return fmt.Errorf("this account has no calendar URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return err
	}
	if cfg.User != "" && len(secret) > 0 {
		req.SetBasicAuth(cfg.User, string(secret))
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("not reachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("the server refused the credentials (%s)", resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("the server answered %s", resp.Status)
	}
	return nil
}
