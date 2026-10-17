package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// Poller.state holds one of these. They are uint32, as the atomic that holds
// them is.
const (
	stateIdle uint32 = iota
	stateRunning
	stateStopping
)

type run struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{}
	wg     sync.WaitGroup
}

// Poller schedules and runs a set of tasks on fixed intervals.
type Poller struct {
	state atomic.Uint32
	run   atomic.Pointer[run]

	tasks   []task
	history History
	logger  *slog.Logger

	mu sync.Mutex
}

// New creates a Poller that uses the given logger for task output. It
// continues the schedule of each task from h.
func New(log *slog.Logger, h History) *Poller {
	if log == nil {
		log = slog.Default()
	}

	p := &Poller{
		tasks:   make([]task, 0),
		history: h,
		logger:  log,
	}

	return p
}

// Register adds a task to the poller. It must be called before Start.
func (p *Poller) Register(t task) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state.Load() != stateIdle {
		return errors.New("cannot register tasks while poller is running or stopping")
	}

	// A negative interval makes the timer fire at once, every time.
	if t.Interval() <= 0 {
		return fmt.Errorf("task %q has interval %s; it must be positive", t.Name(), t.Interval())
	}

	if t.Name() == "" {
		return errors.New("task has empty name")
	}

	p.tasks = append(p.tasks, t)

	return nil
}

// Start runs all registered tasks until ctx is cancelled.
func (p *Poller) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.state.Load() != stateIdle {
		p.mu.Unlock()
		return errors.New("poller is already running or currently stopping")
	}

	ctx, cancel := context.WithCancelCause(ctx)

	r := &run{
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}

	tasks := p.tasks
	p.run.Store(r)
	p.state.Store(stateRunning)

	go func() {
		<-ctx.Done()
		p.stop(r)
	}()

	for _, t := range tasks {
		r.wg.Go(func() {
			p.runTask(r, t)
		})
	}

	p.mu.Unlock()

	<-r.done

	return nil
}

// Stop cancels the current run and waits for all tasks to finish.
func (p *Poller) Stop() {
	p.mu.Lock()
	r := p.run.Load()
	p.mu.Unlock()

	p.stop(r)
}

// stop ends r, but only while r is still the current run.
//
// The identity check makes the watchdog of a run safe. A watchdog can live
// longer than its run. It wakes when its own context is cancelled. But it can
// get scheduled after a later run started. Without the check, it would find
// the poller running and stop the next run.
func (p *Poller) stop(r *run) {
	p.mu.Lock()

	if r == nil || p.state.Load() != stateRunning || p.run.Load() != r {
		p.mu.Unlock()
		return
	}

	p.state.Store(stateStopping)

	cancel, done := r.cancel, r.done

	p.mu.Unlock()

	if cancel != nil {
		cancel(errors.New("poller stop called"))
	}

	r.wg.Wait()

	p.mu.Lock()
	p.state.Store(stateIdle)
	p.mu.Unlock()

	if done != nil {
		close(done)
	}
}

// runTask runs t on its own schedule until the context of r ends. The first
// run is after the wait from firstRun. Each next run is one interval after a
// run finishes. After a run that returned errNotReady, it is notReadyRetry.
// runTask logs a failed run and tries again on schedule. A panic in a run is
// a failed run.
//
// The history records every run that finishes, with or without a failure. So
// after a restart, the wait is the same as for a poller with no restart. A run
// that returned errNotReady did no work. A run that shutdown stopped did not
// finish. So the history records neither, and both run again at the next
// start.
func (p *Poller) runTask(r *run, t task) {
	log := p.logger.With(
		slog.String("task", t.Name()),
		slog.Duration("interval", t.Interval()),
	)

	ctx := r.ctx

	dur := p.firstRun(ctx, t, log)

	log.InfoContext(
		ctx,
		"task scheduled",
		slog.Duration("next_run", dur),
	)

	// Use a timer, because the first wait comes from the history and can be
	// zero. A ticker cannot use zero, and panics on it.
	timer := time.NewTimer(dur)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			// The poller can start under a context that is already cancelled.
			// Then a zero first wait makes both cases ready at once. select
			// picks between ready cases at random. Without this check, the
			// run would start half of the time.
			if ctx.Err() != nil {
				return
			}

			log.InfoContext(ctx, "starting task run")

			next := t.Interval()
			err := runOnce(ctx, t)

			if ctx.Err() == nil && !errors.Is(err, errNotReady) {
				p.record(ctx, t, err, log)
			}

			switch {
			case err == nil:
				log.InfoContext(ctx, "task run completed")
			case errors.Is(err, errNotReady):
				// min: a task with a shorter interval keeps its interval.
				next = min(notReadyRetry, t.Interval())
				log.InfoContext(ctx, "task has nothing to work on yet, retrying soon",
					slog.Duration("next_run", next))
			default:
				log.ErrorContext(
					ctx,
					"task failed during poll, will be retried again",
					logger.Err(err),
				)
			}

			// Reset needs no drain. Since Go 1.23, a timer channel holds no
			// stale value.
			timer.Reset(next)
		}
	}
}

// runOnce runs t one time. It returns a panic in the run as an error. So the
// task stays on its schedule. Without this, one panic would stop the task
// until the next restart.
func runOnce(ctx context.Context, t task) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("task panicked: %v", v)
		}
	}()

	return t.Run(ctx)
}

// firstRun is how long t waits before its first run. This is the rest of its
// interval since its last recorded run. If it has no recorded run, the wait is
// zero.
//
// The wait stays between zero and one interval. A clock that moved gives a
// stored time in the future, or far in the past. Neither is a reason to stop
// the task, or to run it more than once.
//
// If the history cannot be read, the task waits a full interval. If the poller
// does not know whether the work is due, it waits. A run would turn a restart
// loop into a scan loop. The stored schedule exists to prevent this failure.
func (p *Poller) firstRun(ctx context.Context, t task, log *slog.Logger) time.Duration {
	at, err := p.history.LastRun(ctx, t.Name())

	switch {
	case errors.Is(err, inventory.ErrNotFound):
		return 0
	case err != nil:
		log.ErrorContext(ctx, "could not tell when the task last ran, holding off for one interval",
			logger.Err(err),
		)

		return t.Interval()
	}

	return min(max(t.Interval()-time.Since(at), 0), t.Interval())
}

// record stores now as the time when t last finished a run. It stores runErr as
// the reason of a failure. If the store fails, record only logs it. The cost is
// one early run after the next restart.
func (p *Poller) record(ctx context.Context, t task, runErr error, log *slog.Logger) {
	err := p.history.RecordRun(ctx, t.Name(), time.Now(), runErr)
	if err != nil {
		log.ErrorContext(ctx, "could not record the task's run", logger.Err(err))
	}
}
