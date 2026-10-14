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

// Poller.state holds one of these. They are uint32 to match the atomic that
// carries them.
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

// New creates a Poller that uses the given logger for task output, and resumes
// each task's schedule from h.
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

	if t.Interval() == 0 {
		return fmt.Errorf("task %q has zero interval", t.Name())
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

// stop ends r, but only while r is still the run in progress.
//
// The identity check is what makes a run's watchdog safe. A watchdog outlives
// the run it belongs to: it wakes when its own context is cancelled, but need
// not be scheduled before a later run has started. Without the check it would
// find the poller running and tear down its successor.
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

// runTask runs t on its own schedule until r's context ends: first after the
// wait firstRun allows, then one interval after each run finishes, or
// notReadyRetry after a run that returned errNotReady. A failed run is logged
// and retried on schedule. A panic is logged and ends this task alone.
//
// Every run that finishes, failed or not, is recorded in the history, so a
// restart waits what an uninterrupted poller would have. A run that returned
// errNotReady did no work, and one cut short by shutdown did not finish, so
// neither is recorded and both run again at the next start.
func (p *Poller) runTask(r *run, t task) {
	log := p.logger.With(
		slog.String("task", t.Name()),
		slog.Duration("interval", t.Interval()),
	)

	ctx := r.ctx

	defer func() {
		err := recover()
		if err != nil {
			log.ErrorContext(ctx, "task panicked", slog.String("panic", fmt.Sprint(err)))
		}
	}()

	dur := p.firstRun(ctx, t, log)

	log.InfoContext(
		ctx,
		"task scheduled",
		slog.Duration("next_run", dur),
	)

	// A timer, because the first wait comes from the history and may be
	// zero, which a ticker cannot express and panics on.
	timer := time.NewTimer(dur)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			// A zero first wait leaves both cases ready at once when the poller
			// is started under a context that is already cancelled, and select
			// picks between ready cases at random. Without this the run would
			// go ahead half the time.
			if ctx.Err() != nil {
				return
			}

			log.InfoContext(ctx, "starting task run")

			next := t.Interval()
			err := t.Run(ctx)

			if ctx.Err() == nil && !errors.Is(err, errNotReady) {
				p.record(ctx, t, log)
			}

			switch {
			case err == nil:
				log.InfoContext(ctx, "task run completed")
			case errors.Is(err, errNotReady):
				// min: a task whose interval is already shorter keeps it.
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

			// Reset needs no drain: since Go 1.23 a timer channel holds no
			// stale value to receive.
			timer.Reset(next)
		}
	}
}

// firstRun is how long t waits before its first run: what is left of its
// interval since its last recorded run, or nothing when it has none.
//
// The wait is held to between zero and one interval. A stored time in the
// future or far in the past is what a clock that moved looks like, so neither
// is a reason to strand the task or run it more than once.
//
// A history that cannot be read waits a full interval. Not knowing whether
// the work is due is a reason to hold off: running anyway would turn a restart
// loop into a scan loop, the one failure the stored schedule exists to
// prevent.
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

// record stores now as when t last finished a run. A failure is logged and
// otherwise ignored: it costs one early run after the next restart.
func (p *Poller) record(ctx context.Context, t task, log *slog.Logger) {
	err := p.history.RecordRun(ctx, t.Name(), time.Now())
	if err != nil {
		log.ErrorContext(ctx, "could not record the task's run", logger.Err(err))
	}
}
