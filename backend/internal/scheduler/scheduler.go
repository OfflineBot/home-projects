// Package scheduler runs the jobs that pull data into projects.
//
// Its most important job is not the pulling. It is the rule from section 5 of
// the brief: a credential is used **once**. Everything in here is arranged
// around that — reserve before the attempt, confirm only on an unambiguous
// success, delete the secret on every other outcome, and never, ever retry.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/events"
	"github.com/offlinebot/home-projects/backend/internal/filter"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/variables"
	"github.com/robfig/cron/v3"
)

type Runner struct {
	env *capability.Env

	mu      sync.Mutex
	cron    *cron.Cron
	entries map[uuid.UUID]cron.EntryID
	busy    map[uuid.UUID]bool
}

func New(env *capability.Env) *Runner {
	return &Runner{
		env:     env,
		cron:    cron.New(),
		entries: map[uuid.UUID]cron.EntryID{},
		busy:    map[uuid.UUID]bool{},
	}
}

// Start recovers anything that was left in flight and then begins ticking.
func (r *Runner) Start(ctx context.Context) error {
	// A mark that survived a restart means an attempt never came back with a
	// success. The secret counts as used; the account waits for a human.
	consumed, err := r.env.Store.RecoverInFlight(ctx)
	if err != nil {
		return fmt.Errorf("credentials in flight could not be cleaned up: %w", err)
	}
	for _, id := range consumed {
		slog.Warn("credential was consumed by a restart during an attempt", "account", id)
	}
	if err := r.Reload(ctx); err != nil {
		return err
	}
	r.cron.Start()
	return nil
}

func (r *Runner) Stop() {
	ctx := r.cron.Stop()
	<-ctx.Done()
}

// Reload rebuilds the schedule from the database. It runs at startup and after
// every change to a scheduler.
func (r *Runner) Reload(ctx context.Context) error {
	list, err := r.env.Store.ListSchedulers(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	for id, entry := range r.entries {
		r.cron.Remove(entry)
		delete(r.entries, id)
	}
	for _, s := range list {
		if !s.Enabled || s.Schedule == "" || strings.EqualFold(s.Schedule, "manual") {
			continue
		}
		spec := s.Schedule
		id := s.ID
		entry, err := r.cron.AddFunc(spec, func() {
			runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if _, err := r.Run(runCtx, id, "schedule"); err != nil {
				slog.Warn("scheduled run failed", "scheduler", id, "error", err)
			}
		})
		if err != nil {
			slog.Warn("scheduler has an unusable schedule", "scheduler", s.ID, "schedule", spec, "error", err)
			_, _ = r.env.Store.UpdateScheduler(ctx, s.ID, store.SchedulerPatch{
				Enabled:   ptr(false),
				PausedFor: ptr("the schedule " + spec + " cannot be read: " + err.Error()),
			})
			continue
		}
		r.entries[s.ID] = entry
	}
	return nil
}

// ErrAlreadyRunning is what a second start gets while the first is still
// going. It is its own error so the API can answer 409 rather than 500: this is
// not a failure, it is a refusal.
var ErrAlreadyRunning = errors.New("this scheduler is already running")

// Running reports whether a run is in flight. The UI asks so the button can be
// dark before it is pressed, instead of explaining afterwards.
func (r *Runner) Running(id uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy[id]
}

// Next reports when each scheduler runs next, for the UI.
func (r *Runner) Next(id uuid.UUID) *time.Time {
	r.mu.Lock()
	entryID, ok := r.entries[id]
	r.mu.Unlock()
	if !ok {
		return nil
	}
	entry := r.cron.Entry(entryID)
	if entry.Next.IsZero() {
		return nil
	}
	next := entry.Next
	return &next
}

// Run executes one scheduler. trigger is "schedule", "manual" or "automation".
func (r *Runner) Run(ctx context.Context, schedulerID uuid.UUID, trigger string) (*model.SchedulerRun, error) {
	st := r.env.Store

	sched, err := st.SchedulerByID(ctx, schedulerID)
	if err != nil {
		return nil, err
	}

	// One run at a time per scheduler. Two runs writing the same files, with
	// one credential between them, is how a pull half-overwrites itself and a
	// password gets spent twice.
	r.mu.Lock()
	if r.busy[schedulerID] {
		r.mu.Unlock()
		return nil, ErrAlreadyRunning
	}
	r.busy[schedulerID] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.busy, schedulerID)
		r.mu.Unlock()
	}()

	runID, err := st.StartRun(ctx, schedulerID, trigger)
	if err != nil {
		return nil, err
	}

	var logLines []string
	logf := func(format string, args ...any) {
		line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, args...)
		logLines = append(logLines, line)
	}

	finish := func(status, message string, changed int) (*model.SchedulerRun, error) {
		_ = st.FinishRun(ctx, runID, status, message, changed, strings.Join(logLines, "\n"))
		_, _ = st.UpdateScheduler(ctx, schedulerID, store.SchedulerPatch{
			LastStatus: ptr(status), MarkRunNow: true,
		})
		r.env.Bus.Publish(events.Event{
			Kind:      events.SchedulerFinished,
			ProjectID: sched.ProjectID,
			Detail: map[string]any{
				"scheduler": schedulerID.String(), "status": status, "message": message,
				"kind": sched.Kind,
			},
		})
		runs, err := st.ListRuns(ctx, schedulerID, 1)
		if err != nil || len(runs) == 0 {
			return nil, err
		}
		if status == "error" {
			return &runs[0], fmt.Errorf("%s", message)
		}
		return &runs[0], nil
	}

	project, err := st.ProjectByID(ctx, sched.ProjectID)
	if err != nil {
		return finish("error", "the target project no longer exists", 0)
	}
	var group *model.Group
	if project.GroupID != nil {
		group, _ = st.GroupByID(ctx, *project.GroupID)
	}
	// A frozen project does not get written into — and the scheduler says so
	// instead of failing quietly.
	if project.EffectiveReadOnly(group) {
		_, _ = st.UpdateScheduler(ctx, schedulerID, store.SchedulerPatch{
			Enabled:   ptr(false),
			PausedFor: ptr("the project " + project.Title + " is read-only"),
		})
		return finish("error", "the project "+project.Title+" is read-only, so nothing was written", 0)
	}

	kind, ok := capability.SchedulerKindByName(sched.Kind)
	if !ok {
		return finish("error", fmt.Sprintf("no scheduler of kind %q is installed", sched.Kind), 0)
	}

	options := map[string]any{}
	if len(sched.Options) > 0 {
		_ = json.Unmarshal(sched.Options, &options)
	}

	job := capability.Job{
		Scheduler: sched,
		Project:   project,
		Options:   options,
		Trigger:   trigger,
		Log:       logf,
	}

	// A filter, if this scheduler points at one. The capability is handed a
	// function, not a table: it asks where something belongs and gets an
	// answer, and never learns what a filter is.
	if len(sched.FilterIDs) > 0 {
		// Several filters run as one long list of rules, in the order they were
		// given: the first rule that matches takes the file. That is what makes
		// "one filter for the first semester, one for the second" work.
		var rules []filter.Rule
		for _, id := range sched.FilterIDs {
			f, err := st.FilterByID(ctx, id)
			if err != nil {
				return finish("error", "a filter this scheduler uses no longer exists", 0)
			}
			var part []filter.Rule
			if err := json.Unmarshal(f.Rules, &part); err != nil {
				return finish("error", "the rules of "+f.Title+" could not be read", 0)
			}
			logf("filter %q: %d rule(s)", f.Title, len(part))
			rules = append(rules, part...)
		}
		job.Route = func(items []capability.RouteItem) []capability.RouteTo {
			in := make([]filter.Item, len(items))
			for i, item := range items {
				in[i] = filter.Item{Name: item.Name, Path: item.Path, Semester: item.Semester}
			}
			out := make([]capability.RouteTo, len(items))
			for i, d := range filter.Plan(rules, in) {
				out[i] = capability.RouteTo{
					Project: d.Project, Folder: d.Folder, Skip: d.Skip,
					Matched: d.Matched, Rule: d.Rule,
				}
			}
			return out
		}
	}

	// ---- the credential half -------------------------------------------
	var account *model.Account
	if sched.AccountID != nil {
		account, err = st.AccountByID(ctx, *sched.AccountID)
		if err != nil {
			return finish("error", "the account this scheduler uses no longer exists", 0)
		}
		if account.NeedsSecret {
			_, _ = st.UpdateScheduler(ctx, schedulerID, store.SchedulerPatch{
				Enabled:   ptr(false),
				PausedFor: ptr("credential missing: enter the password for " + account.Title + " again"),
			})
			return finish("error",
				"the password for "+account.Title+" was used up. Enter it again — there is no automatic second attempt.", 0)
		}

		// Whatever can be checked without the credential is checked first, so a
		// wrong address costs a run and not a password.
		if kind, ok := capability.AccountKindByName(account.Kind); ok && kind.Precheck != nil {
			if err := kind.Precheck(ctx, r.env, account); err != nil {
				logf("stopped before the credential was touched: %v", err)
				return finish("error", err.Error()+" — the stored password was not touched", 0)
			}
		}

		// Reserve first: persistently, before anything is sent.
		secret, err := st.ReserveAttempt(ctx, *sched.AccountID)
		if err != nil {
			return finish("error", err.Error(), 0)
		}
		plain, err := r.env.Box.Open(secret)
		if err != nil {
			// We cannot even read it — that still counts as an attempt.
			_ = st.ConsumeSecret(ctx, *sched.AccountID,
				"the stored password could not be decrypted with the configured key")
			return finish("error", "the stored password could not be decrypted — enter it again", 0)
		}
		job.Account = account
		job.Secret = plain
		logf("credential for %q reserved — this is the only attempt", account.Title)
	}

	var report capability.Report
	var runErr error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				runErr = fmt.Errorf("the run crashed: %v", rec)
			}
		}()
		report, runErr = kind.Run(ctx, r.env, job)
	}()

	if account != nil {
		if runErr == nil && report.Authenticated {
			if err := st.ConfirmSuccess(ctx, account.ID); err != nil {
				slog.Error("success could not be recorded", "account", account.ID, "error", err)
			}
			logf("sign-in confirmed, credential kept")
		} else {
			reason := "the attempt did not end in a confirmed sign-in"
			if runErr != nil {
				reason = runErr.Error()
			} else if !report.Authenticated {
				reason = "the run did not confirm a sign-in"
			}
			if err := st.ConsumeSecret(ctx, account.ID, reason); err != nil {
				slog.Error("credential could not be removed", "account", account.ID, "error", err)
			}
			logf("credential used up: %s", reason)
			logf("no second attempt will be made — the password has to be entered again")
		}
	}

	if runErr != nil {
		return finish("error", runErr.Error(), report.FilesChanged)
	}

	// What arrived is now the project's business. A filter it picked up and
	// marked automatic runs here — the scheduler never learns what a filter is,
	// and a project nothing fetches into can use the same one by hand.
	if report.FilesChanged > 0 && r.env.SortProject != nil {
		moved, err := r.env.SortProject(ctx, project)
		switch {
		case err != nil:
			logf("the project's own filters could not be run: %v", err)
		case moved > 0:
			logf("the project's filters moved %d thing(s) into place", moved)
		}
	}

	for _, v := range report.Variables {
		if v.Source == "" {
			v.Source = "scheduler:" + sched.Kind
		}
		if err := variables.Set(ctx, r.env, project.ID, v); err != nil {
			logf("variable %s could not be stored: %v", v.Name, err)
		}
	}

	message := report.Message
	if message == "" {
		message = "done"
	}
	return finish("ok", message, report.FilesChanged)
}

func ptr[T any](v T) *T { return &v }
