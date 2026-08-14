package scheduler_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/scheduler"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// slowCapability blocks inside its run until the test lets it go, which is the
// only way to ask "what happens while one is still running?".
type slowCapability struct{ capability.Base }

var (
	slowStarted = make(chan struct{}, 4)
	slowRelease = make(chan struct{})
	slowRuns    int
	slowMu      sync.Mutex
)

func (slowCapability) Name() string  { return "slow-test" }
func (slowCapability) Title() string { return "Slow test" }
func (slowCapability) Icon() string  { return "clock" }

func (slowCapability) SchedulerKinds() []capability.SchedulerKind {
	return []capability.SchedulerKind{{
		Name:  "slow-test",
		Title: "Slow test fetch",
		Run: func(ctx context.Context, env *capability.Env, job capability.Job) (capability.Report, error) {
			slowMu.Lock()
			slowRuns++
			slowMu.Unlock()
			slowStarted <- struct{}{}
			<-slowRelease
			return capability.Report{Message: "done"}, nil
		},
	}}
}

// TestASecondStartIsRefused is the rule: one run at a time per scheduler.
//
// Two runs would write the same files at the same time, and — for a scheduler
// with an account — would spend one single-use credential twice. The second
// start is therefore refused outright rather than queued behind the first,
// because a queued run is a run nobody asked for a second time.
func TestASecondStartIsRefused(t *testing.T) {
	h := setup(t)
	if !capability.Exists("slow-test") {
		capability.Register(slowCapability{})
	}
	ctx := context.Background()

	sched, err := h.store.CreateScheduler(ctx, store.NewScheduler{
		OwnerID: h.ownerID, ProjectID: h.project.ID,
		Title: "slow", Kind: "slow-test", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatalf("scheduler: %v", err)
	}

	slowMu.Lock()
	slowRuns = 0
	slowMu.Unlock()
	slowRelease = make(chan struct{})

	first := make(chan error, 1)
	go func() {
		_, err := h.runner.Run(ctx, sched.ID, "manual")
		first <- err
	}()

	// Wait until the first run is genuinely inside the capability.
	select {
	case <-slowStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("the first run never started")
	}

	if !h.runner.Running(sched.ID) {
		t.Error("a run in flight has to be visible as one — the button is drawn from this")
	}

	_, err = h.runner.Run(ctx, sched.ID, "manual")
	if !errors.Is(err, scheduler.ErrAlreadyRunning) {
		t.Fatalf("a second start while the first is running: got %v, want ErrAlreadyRunning", err)
	}

	close(slowRelease)
	if err := <-first; err != nil {
		t.Fatalf("the first run should have finished cleanly: %v", err)
	}

	slowMu.Lock()
	runs := slowRuns
	slowMu.Unlock()
	if runs != 1 {
		t.Errorf("the capability ran %d times, want exactly 1", runs)
	}
	if h.runner.Running(sched.ID) {
		t.Error("after it finished, it is not running any more")
	}

	// And then it can be run again — a refusal is not a lock left behind.
	slowRelease = make(chan struct{})
	close(slowRelease)
	if _, err := h.runner.Run(ctx, sched.ID, "manual"); err != nil {
		t.Fatalf("a run after the first finished: %v", err)
	}
}

// The trigger reaches the capability, because "rebuild" is how a run is told to
// make the target match the source instead of adding to it.
func TestTriggerReachesTheJob(t *testing.T) {
	h := setup(t)
	seen := make(chan string, 2)
	if !capability.Exists("trigger-test") {
		capability.Register(triggerCapability{seen: seen})
	}
	ctx := context.Background()
	sched, err := h.store.CreateScheduler(ctx, store.NewScheduler{
		OwnerID: h.ownerID, ProjectID: h.project.ID,
		Title: "trigger", Kind: "trigger-test", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatalf("scheduler: %v", err)
	}
	for _, want := range []string{"manual", "rebuild"} {
		if _, err := h.runner.Run(ctx, sched.ID, want); err != nil {
			t.Fatalf("run %s: %v", want, err)
		}
		select {
		case got := <-seen:
			if got != want {
				t.Errorf("the job was told %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the run never reported its trigger")
		}
	}
}

type triggerCapability struct {
	capability.Base
	seen chan string
}

func (c triggerCapability) Name() string  { return "trigger-test" }
func (c triggerCapability) Title() string { return "Trigger test" }
func (c triggerCapability) Icon() string  { return "clock" }

func (c triggerCapability) SchedulerKinds() []capability.SchedulerKind {
	return []capability.SchedulerKind{{
		Name:  "trigger-test",
		Title: "Trigger test fetch",
		Run: func(ctx context.Context, env *capability.Env, job capability.Job) (capability.Report, error) {
			c.seen <- job.Trigger
			return capability.Report{Message: "done"}, nil
		},
	}}
}
