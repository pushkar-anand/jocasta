package poller

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tick is the interval every fake task runs at. Short enough that a test can
// observe several runs without sleeping, long enough that a slow CI box does
// not turn a single expected run into three.
const tick = 5 * time.Millisecond

// settle is how long to wait for a lifecycle transition that has no signal of
// its own to wait on. Generous on purpose: it only bounds how long a broken
// poller takes to fail the test, and these tests run in parallel under -race
// where a 5ms ticker can slip well past a tight budget.
const settle = 5 * time.Second

// fake is a task whose behaviour each test dictates: how often it runs, what
// it returns, and how many times it was called.
type fake struct {
	name     string
	interval time.Duration
	runs     atomic.Int32

	// err is returned from every Run, to check a failing task is retried
	// rather than retired.
	err error

	// panics makes Run panic, to check one task's panic does not take the
	// process down with it.
	panics bool

	// block, when non-nil, holds Run until it is closed or the context ends,
	// so a test can catch the poller mid-run.
	block chan struct{}

	// due is what DueIn reports. Zero, the default, is a task with nothing to
	// resume from, which runs at once.
	due time.Duration
}

func (f *fake) DueIn(context.Context) time.Duration { return f.due }

func (f *fake) Name() string            { return f.name }
func (f *fake) Interval() time.Duration { return f.interval }

func (f *fake) Run(ctx context.Context) error {
	f.runs.Add(1)

	if f.panics {
		panic("task panicked on purpose")
	}

	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
		}
	}

	return f.err
}

var _ task = (*fake)(nil)

func newFake(name string) *fake {
	return &fake{name: name, interval: tick}
}

func newPoller(t *testing.T, tasks ...task) *Poller {
	t.Helper()

	p := New(slog.New(slog.DiscardHandler))

	for _, task := range tasks {
		require.NoError(t, p.Register(task))
	}

	return p
}

// startAsync runs Start in the background and returns a channel carrying its
// error, so a test can assert that Start actually returns on shutdown rather
// than hanging.
func startAsync(ctx context.Context, t *testing.T, p *Poller) <-chan error {
	t.Helper()

	errc := make(chan error, 1)

	go func() { errc <- p.Start(ctx) }()

	return errc
}

// requireReturned fails the test if Start has not returned within settle.
func requireReturned(t *testing.T, errc <-chan error) {
	t.Helper()

	select {
	case err := <-errc:
		require.NoError(t, err)
	case <-time.After(settle):
		require.FailNow(t, "Start did not return")
	}
}

// eventually waits for cond, so a test never depends on a fixed sleep being
// long enough on a loaded machine.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()

	assert.Eventually(t, cond, settle, time.Millisecond, msg)
}

func TestRegisterRejectsZeroInterval(t *testing.T) {
	t.Parallel()

	p := New(slog.New(slog.DiscardHandler))

	err := p.Register(&fake{name: "zero", interval: 0})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero interval")
}

func TestRegisterRejectsEmptyName(t *testing.T) {
	t.Parallel()

	p := New(slog.New(slog.DiscardHandler))

	err := p.Register(&fake{name: "", interval: tick})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty name")
}

func TestRegisterAcceptsValidTaskWhenIdle(t *testing.T) {
	t.Parallel()

	p := New(slog.New(slog.DiscardHandler))

	require.NoError(t, p.Register(newFake("a")))
	require.NoError(t, p.Register(newFake("b")))

	assert.Len(t, p.tasks, 2)
}

// Registering after Start would leave a task that never runs, because Start
// takes its own copy of the slice.
func TestRegisterRejectedWhileRunning(t *testing.T) {
	t.Parallel()

	f := newFake("a")
	p := newPoller(t, f)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errc := startAsync(ctx, t, p)

	eventually(t, func() bool { return f.runs.Load() > 0 }, "task never ran")

	err := p.Register(newFake("late"))
	require.Error(t, err)

	cancel()
	requireReturned(t, errc)
}

func TestStartRunsEveryRegisteredTask(t *testing.T) {
	t.Parallel()

	a, b := newFake("a"), newFake("b")
	p := newPoller(t, a, b)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errc := startAsync(ctx, t, p)

	eventually(t, func() bool { return a.runs.Load() > 0 && b.runs.Load() > 0 },
		"both tasks should have run")

	cancel()
	requireReturned(t, errc)
}

// A task is scheduled by its own interval, so a slow task cannot delay a fast
// one.
func TestTasksRunOnIndependentIntervals(t *testing.T) {
	t.Parallel()

	fast := &fake{name: "fast", interval: tick}
	slow := &fake{name: "slow", interval: 10 * tick}
	p := newPoller(t, fast, slow)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errc := startAsync(ctx, t, p)

	eventually(t, func() bool { return fast.runs.Load() >= 5 }, "fast task should have run repeatedly")

	assert.Less(t, slow.runs.Load(), fast.runs.Load(),
		"slow task should not keep pace with the fast one")

	cancel()
	requireReturned(t, errc)
}

func TestStartRejectsSecondStart(t *testing.T) {
	t.Parallel()

	f := newFake("a")
	p := newPoller(t, f)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errc := startAsync(ctx, t, p)

	eventually(t, func() bool { return f.runs.Load() > 0 }, "task never ran")

	err := p.Start(t.Context())
	require.Error(t, err)

	cancel()
	requireReturned(t, errc)
}

// Cancelling the context handed to Start is the ordinary shutdown path, and it
// must unblock Start without anyone calling Stop.
func TestParentCancelStopsPoller(t *testing.T) {
	t.Parallel()

	f := newFake("a")
	p := newPoller(t, f)

	ctx, cancel := context.WithCancel(t.Context())
	errc := startAsync(ctx, t, p)

	eventually(t, func() bool { return f.runs.Load() > 0 }, "task never ran")

	cancel()
	requireReturned(t, errc)

	assert.Equal(t, uint32(stateIdle), p.state.Load(), "poller should be idle after shutdown")
}

// Stop is reachable from an API handler, so it must unblock Start on its own.
func TestStopUnblocksStart(t *testing.T) {
	t.Parallel()

	f := newFake("a")
	p := newPoller(t, f)

	errc := startAsync(t.Context(), t, p)

	eventually(t, func() bool { return f.runs.Load() > 0 }, "task never ran")

	p.Stop()
	requireReturned(t, errc)

	assert.Equal(t, uint32(stateIdle), p.state.Load(), "poller should be idle after Stop")
}

// Stop races itself: the watchdog Start spawns calls it too, and callers hold
// a deferred Stop. Every one of those must be safe.
func TestStopIsIdempotent(t *testing.T) {
	t.Parallel()

	f := newFake("a")
	p := newPoller(t, f)

	errc := startAsync(t.Context(), t, p)

	eventually(t, func() bool { return f.runs.Load() > 0 }, "task never ran")

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			p.Stop()
		})
	}

	wg.Wait()
	requireReturned(t, errc)
}

func TestStopBeforeStartIsSafe(t *testing.T) {
	t.Parallel()

	p := newPoller(t, newFake("a"))

	assert.NotPanics(t, p.Stop)
	assert.Equal(t, uint32(stateIdle), p.state.Load())
}

// Stopping must leave the poller reusable, which is the whole reason Stop
// returns it to idle rather than to a terminal state.
func TestStartAfterStop(t *testing.T) {
	t.Parallel()

	f := newFake("a")
	p := newPoller(t, f)

	for range 3 {
		errc := startAsync(t.Context(), t, p)

		eventually(t, func() bool { return f.runs.Load() > 0 }, "task never ran")

		p.Stop()
		requireReturned(t, errc)

		f.runs.Store(0)
	}
}

// Tasks must be gone by the time Start returns, otherwise a caller that
// reopens the database underneath them is racing a live sweep.
func TestTasksStopBeforeStartReturns(t *testing.T) {
	t.Parallel()

	f := newFake("a")
	p := newPoller(t, f)

	ctx, cancel := context.WithCancel(t.Context())
	errc := startAsync(ctx, t, p)

	eventually(t, func() bool { return f.runs.Load() > 0 }, "task never ran")

	cancel()
	requireReturned(t, errc)

	settled := f.runs.Load()

	time.Sleep(5 * tick)

	assert.Equal(t, settled, f.runs.Load(), "no task should run after Start returns")
}

// A failing task is logged and retried, never retired: one unreachable network
// must not end the schedule.
func TestFailingTaskKeepsRunning(t *testing.T) {
	t.Parallel()

	f := &fake{name: "failing", interval: tick, err: errors.New("boom")}
	p := newPoller(t, f)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errc := startAsync(ctx, t, p)

	eventually(t, func() bool { return f.runs.Load() >= 3 }, "failing task should be retried")

	cancel()
	requireReturned(t, errc)
}

// A panicking task must not take the process down, and must not strand the
// other tasks or the shutdown path.
func TestPanickingTaskDoesNotKillPoller(t *testing.T) {
	t.Parallel()

	boom := &fake{name: "boom", interval: tick, panics: true}
	ok := newFake("ok")
	p := newPoller(t, boom, ok)

	ctx, cancel := context.WithCancel(t.Context())
	errc := startAsync(ctx, t, p)

	eventually(t, func() bool { return boom.runs.Load() > 0 }, "panicking task never ran")
	eventually(t, func() bool { return ok.runs.Load() > 0 }, "healthy task never ran")

	cancel()
	requireReturned(t, errc)
}

// A run in flight when the context ends must be handed the cancellation, so a
// long sweep aborts instead of holding shutdown open.
func TestRunInFlightSeesCancellation(t *testing.T) {
	t.Parallel()

	f := &fake{name: "blocking", interval: tick, block: make(chan struct{})}
	p := newPoller(t, f)

	ctx, cancel := context.WithCancel(t.Context())
	errc := startAsync(ctx, t, p)

	eventually(t, func() bool { return f.runs.Load() > 0 }, "task never ran")

	cancel()
	requireReturned(t, errc)
}

// The interval is the gap between runs, so a task that overruns its interval
// does not accumulate a backlog of immediate repeats.
func TestOverrunningTaskDoesNotBacklog(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	f := &fake{name: "overrun", interval: tick, block: release}
	p := newPoller(t, f)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errc := startAsync(ctx, t, p)

	eventually(t, func() bool { return f.runs.Load() == 1 }, "task never ran")

	// Hold the run open for many intervals, then release it.
	time.Sleep(10 * tick)
	assert.Equal(t, int32(1), f.runs.Load(), "a held run must not be re-entered")

	close(release)

	// After the release the next run is one interval away, not immediate x10.
	time.Sleep(3 * tick)
	assert.Less(t, f.runs.Load(), int32(10), "ticks must not have accumulated while the run was held")

	cancel()
	requireReturned(t, errc)
}

// A task with nothing to resume from runs at once rather than sitting out its
// first interval, which is a whole interval of staleness otherwise.
func TestTaskDueNowRunsAtStart(t *testing.T) {
	t.Parallel()

	// An interval far longer than the wait below, so that only a run at start
	// can satisfy it: with a short one, a task that waited a full interval
	// would pass too.
	f := &fake{name: "a", interval: time.Hour}
	p := newPoller(t, f)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errc := startAsync(ctx, t, p)

	eventually(t, func() bool { return f.runs.Load() > 0 }, "task did not run at start")

	cancel()
	requireReturned(t, errc)
}

// A zero first wait leaves the timer and the context both ready on the first
// select, which picks at random. Repeated so that a missing ctx.Err() guard
// cannot pass by winning the coin toss once.
func TestStartUnderACancelledContextRunsNothing(t *testing.T) {
	t.Parallel()

	for range 20 {
		f := newFake("a")
		p := newPoller(t, f)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		require.NoError(t, p.Start(ctx))
		assert.Zero(t, f.runs.Load(), "a poller started under a dead context must not run a task")
	}
}

// A task that says its run is due later is left alone until then, which is what
// stops a restart from re-running work that is not due.
func TestTaskDueLaterIsLeftAlone(t *testing.T) {
	t.Parallel()

	f := &fake{name: "a", interval: 100 * tick, due: 100 * tick}
	p := newPoller(t, f)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errc := startAsync(ctx, t, p)

	time.Sleep(5 * tick)
	assert.Zero(t, f.runs.Load(), "a task due later must not run at start")

	cancel()
	requireReturned(t, errc)
}

// A stored time that is wrong in either direction -- a clock that moved, a row
// from the future -- must not strand the task or stampede it.
func TestFirstRunIsClamped(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		due  time.Duration
		want time.Duration
	}{
		"negative is now":     {due: -time.Hour, want: 0},
		"beyond one interval": {due: time.Hour, want: 100 * tick},
		"within the interval": {due: 50 * tick, want: 50 * tick},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := &fake{name: "a", interval: 100 * tick, due: tt.due}

			assert.Equal(t, tt.want, firstRun(t.Context(), f))
		})
	}
}

// Start, Stop and Register hammering the lifecycle at once, to catch races
// under -race. Start blocks until the poller stops, so each round starts it
// once and races several Stops and Registers against it.
func TestConcurrentLifecycle(t *testing.T) {
	t.Parallel()

	p := newPoller(t, newFake("a"), newFake("b"))

	for range 50 {
		errc := startAsync(t.Context(), t, p)

		var wg sync.WaitGroup

		for range 4 {
			wg.Go(func() {
				p.Stop()
			})
		}

		for range 2 {
			wg.Go(func() {
				_ = p.Register(newFake("late"))
			})
		}

		wg.Wait()

		// A Stop that lands before Start published is a no-op, so the poller
		// may still be running here. Stop again until Start lets go.
		for {
			select {
			case err := <-errc:
				require.NoError(t, err)

				goto next
			case <-time.After(10 * time.Millisecond):
				p.Stop()
			}
		}

	next:
	}
}

// A run's watchdog outlives its run: it wakes when its own context is
// cancelled, but need not be scheduled before a later run has started. Calling
// stop with that stale run must not tear down its successor.
//
// The scenario is driven directly rather than by racing the real watchdog,
// which fires too promptly to reproduce it reliably.
func TestStopIgnoresStaleRun(t *testing.T) {
	t.Parallel()

	f := newFake("a")
	p := newPoller(t, f)

	first := startAsync(t.Context(), t, p)

	eventually(t, func() bool { return f.runs.Load() > 0 }, "task never ran")

	stale := p.run.Load()
	require.NotNil(t, stale)

	p.Stop()
	requireReturned(t, first)

	second := startAsync(t.Context(), t, p)

	eventually(t, func() bool { return p.state.Load() == uint32(stateRunning) },
		"second run never started")

	// Exactly what a descheduled watchdog from the first run would do.
	p.stop(stale)

	select {
	case <-second:
		require.FailNow(t, "stale run stopped its successor")
	case <-time.After(20 * tick):
	}

	assert.Equal(t, uint32(stateRunning), p.state.Load(), "second run should still be running")

	f.runs.Store(0)
	eventually(t, func() bool { return f.runs.Load() > 0 }, "second run's task stopped")

	p.Stop()
	requireReturned(t, second)
}
