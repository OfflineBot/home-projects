package scheduler_test

// The test the brief calls non-negotiable (section 5 and section 12):
//
//	after a failed attempt there must be no second one in the log, and the
//	secret must be gone from the database.
//
// It runs against a real database, because the rule is enforced in SQL. Set
// TEST_DATABASE_URL (make test does it for you); without it the test fails
// loudly rather than passing quietly, since a silently skipped test would be
// exactly the kind of green that means nothing.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/accounts"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/config"
	"github.com/offlinebot/home-projects/backend/internal/db"
	"github.com/offlinebot/home-projects/backend/internal/events"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/gitsrv"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/scheduler"
	"github.com/offlinebot/home-projects/backend/internal/secret"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

// attempts counts every time the credential actually reached a login. It is
// the number the test is really about.
var attempts struct {
	sync.Mutex
	seen []string
}

func record(secret string) {
	attempts.Lock()
	defer attempts.Unlock()
	attempts.seen = append(attempts.seen, secret)
}

func attemptCount() int {
	attempts.Lock()
	defer attempts.Unlock()
	return len(attempts.seen)
}

func resetAttempts() {
	attempts.Lock()
	defer attempts.Unlock()
	attempts.seen = nil
}

// lockingCapability stands in for Dualis: an account kind and a scheduler kind
// whose sign-in always fails.
type lockingCapability struct{ capability.Base }

func (lockingCapability) Name() string  { return "locking-test" }
func (lockingCapability) Title() string { return "Locking test" }
func (lockingCapability) Icon() string  { return "lock" }

func (lockingCapability) AccountKinds() []capability.AccountKind {
	return []capability.AccountKind{{
		Name:        "locking-test",
		Title:       "Locking test account",
		SecretLabel: "Password",
		Locks:       true,
		Test: func(ctx context.Context, env *capability.Env, a *model.Account, s []byte) error {
			record(string(s))
			return fmt.Errorf("wrong password")
		},
	}}
}

func (lockingCapability) SchedulerKinds() []capability.SchedulerKind {
	return []capability.SchedulerKind{{
		Name:            "locking-test",
		Title:           "Locking test fetch",
		AccountKinds:    []string{"locking-test"},
		AccountRequired: true,
		Run: func(ctx context.Context, env *capability.Env, job capability.Job) (capability.Report, error) {
			record(string(job.Secret))
			// No success is confirmed: this is exactly the "ambiguous or
			// failed" case the rule is about.
			return capability.Report{}, fmt.Errorf("sign-in failed")
		},
	}}
}

type harness struct {
	env     *capability.Env
	runner  *scheduler.Runner
	store   *store.Store
	ownerID uuid.UUID
	project *model.Project
}

func setup(t *testing.T) *harness {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL is not set — this test needs a database, and skipping it would be a false green. Run `make test`.")
	}

	dir := t.TempDir()
	t.Setenv("DATABASE_URL", url)
	t.Setenv("JWT_SECRET", "test-jwt-secret-long-enough")
	t.Setenv("SECRET_KEY", "test-secret-key-long-enough")
	t.Setenv("DATA_DIR", dir+"/data")
	t.Setenv("GIT_DIR", dir+"/git")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := db.MigrateCore(ctx, pool); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	st := store.New(pool)
	box, err := secret.NewBox(cfg.SecretKey)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.NewStore(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	git := gitsrv.New(cfg, ws)
	env := &capability.Env{
		Cfg: cfg, Store: st, Files: files.New(st, ws, git, bus), Bus: bus, Box: box,
	}
	env.UseAccount = func(ctx context.Context, id uuid.UUID, fn func([]byte) error) error {
		return accounts.Attempt(ctx, env, id, time.Minute, fn)
	}

	if !capability.Exists("locking-test") {
		capability.Register(lockingCapability{})
	}

	// A user, a project — the little the scheduler needs.
	hash, err := secret.Hash("password-for-the-test")
	if err != nil {
		t.Fatal(err)
	}
	name := "test-" + uuid.NewString()[:8]
	user, err := st.CreateUser(ctx, name, hash, name, false)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	project, err := st.CreateProject(ctx, store.NewProject{
		OwnerID: user.ID, Slug: "p-" + uuid.NewString()[:8], Title: "Test project",
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	t.Cleanup(func() {
		_ = st.DeleteProject(context.Background(), project.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, user.ID)
	})

	resetAttempts()
	return &harness{env: env, runner: scheduler.New(env), store: st, ownerID: user.ID, project: project}
}

func (h *harness) account(t *testing.T, password string) *model.Account {
	t.Helper()
	sealed, err := h.env.Box.Seal([]byte(password))
	if err != nil {
		t.Fatal(err)
	}
	a, err := h.store.CreateAccount(context.Background(), h.ownerID, "locking-test",
		"Test account", json.RawMessage(`{"user":"someone"}`), sealed)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	t.Cleanup(func() { _ = h.store.DeleteAccount(context.Background(), a.ID) })
	return a
}

func (h *harness) scheduler(t *testing.T, accountID uuid.UUID) *model.Scheduler {
	t.Helper()
	s, err := h.store.CreateScheduler(context.Background(), store.NewScheduler{
		OwnerID: h.ownerID, ProjectID: h.project.ID, AccountID: &accountID,
		Title: "test", Kind: "locking-test", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatalf("scheduler: %v", err)
	}
	return s
}

// TestFailedAttemptConsumesTheSecret is the rule itself.
func TestFailedAttemptConsumesTheSecret(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	account := h.account(t, "the-only-password")
	sched := h.scheduler(t, account.ID)

	run, err := h.runner.Run(ctx, sched.ID, "manual")
	if err == nil {
		t.Fatal("the run should have failed")
	}
	if run == nil || run.Status != "error" {
		t.Fatalf("the failed run is not in the log: %+v", run)
	}

	if got := attemptCount(); got != 1 {
		t.Fatalf("the credential was used %d times, expected exactly 1", got)
	}

	after, err := h.store.AccountByID(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.HasSecret {
		t.Error("the secret is still in the database after a failed attempt")
	}
	if !after.NeedsSecret {
		t.Error("the account does not ask for the password again")
	}
	if after.State != "needs_password" {
		t.Errorf("state = %q, expected needs_password", after.State)
	}
	if after.AttemptInFlight {
		t.Error("the attempt mark was not cleared")
	}

	// The schedulers hanging off it are paused, and named.
	list, err := h.store.ListSchedulersForAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Enabled {
		t.Fatalf("the scheduler was not paused: %+v", list)
	}
	if list[0].PausedFor == "" {
		t.Error("the pause has no reason a human could read")
	}
}

// TestNoSecondAttempt is the other half: nothing tries again on its own.
func TestNoSecondAttempt(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	account := h.account(t, "the-only-password")
	sched := h.scheduler(t, account.ID)

	_, _ = h.runner.Run(ctx, sched.ID, "manual")

	// Every route that could produce a second attempt is tried here.
	if _, err := h.runner.Run(ctx, sched.ID, "manual"); err == nil {
		t.Error("a second run was allowed")
	}
	if _, err := h.runner.Run(ctx, sched.ID, "schedule"); err == nil {
		t.Error("a scheduled run was allowed")
	}
	if err := accounts.Test(ctx, h.env, account.ID); err == nil {
		t.Error("a connection test was allowed")
	}
	if err := h.env.UseAccount(ctx, account.ID, func([]byte) error { return nil }); err == nil {
		t.Error("an automation action was allowed to use the account")
	}

	if got := attemptCount(); got != 1 {
		t.Fatalf("the credential reached a login %d times, expected exactly 1", got)
	}
}

// TestTestConnectionIsTheSameAttempt — no blind testing.
func TestTestConnectionIsTheSameAttempt(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	account := h.account(t, "the-only-password")

	if err := accounts.Test(ctx, h.env, account.ID); err == nil {
		t.Fatal("the test should have failed")
	}
	after, err := h.store.AccountByID(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.HasSecret || !after.NeedsSecret {
		t.Error("a failed connection test did not consume the credential")
	}
	if got := attemptCount(); got != 1 {
		t.Fatalf("the test made %d attempts, expected 1", got)
	}
}

// TestOnlyReEntryHelps: there is no way back other than typing it in again.
func TestOnlyReEntryHelps(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	account := h.account(t, "the-only-password")
	sched := h.scheduler(t, account.ID)

	_, _ = h.runner.Run(ctx, sched.ID, "manual")

	sealed, err := h.env.Box.Seal([]byte("a-new-password"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetAccountSecret(ctx, account.ID, sealed); err != nil {
		t.Fatal(err)
	}
	after, err := h.store.AccountByID(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.HasSecret || after.NeedsSecret {
		t.Fatal("entering the password again did not restore the account")
	}
	list, _ := h.store.ListSchedulersForAccount(ctx, account.ID)
	if len(list) != 1 || !list[0].Enabled {
		t.Error("the scheduler was not resumed after the password was entered again")
	}

	// And the new password is used exactly once as well.
	_, _ = h.runner.Run(ctx, sched.ID, "manual")
	if got := attemptCount(); got != 2 {
		t.Fatalf("expected two attempts in total (one per password), got %d", got)
	}
	attempts.Lock()
	seen := append([]string{}, attempts.seen...)
	attempts.Unlock()
	if seen[0] != "the-only-password" || seen[1] != "a-new-password" {
		t.Errorf("a password was reused: %v", seen)
	}
}

// TestOneAttemptAtATime: two parallel runs never both get the credential.
func TestOneAttemptAtATime(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	account := h.account(t, "the-only-password")

	first, err := h.store.ReserveAttempt(ctx, account.ID)
	if err != nil {
		t.Fatalf("the first reservation failed: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("the reservation handed out no secret")
	}
	if _, err := h.store.ReserveAttempt(ctx, account.ID); err == nil {
		t.Fatal("a second reservation was allowed while one was in flight")
	}
}

// TestCrashDuringAttemptConsumes: a mark that survives a restart means the
// attempt never came back, so the password counts as used.
func TestCrashDuringAttemptConsumes(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	account := h.account(t, "the-only-password")

	if _, err := h.store.ReserveAttempt(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	// … and here the server dies. The next start finds the mark.
	consumed, err := h.store.RecoverInFlight(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range consumed {
		if id == account.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("the account in flight was not recovered")
	}
	after, err := h.store.AccountByID(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.HasSecret || !after.NeedsSecret {
		t.Error("a crash during an attempt did not consume the credential")
	}
	if after.LastError == "" {
		t.Error("the account does not say why it is locked")
	}
}
