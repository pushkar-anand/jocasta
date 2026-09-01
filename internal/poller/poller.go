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
)

const (
	stateIdle = iota
	stateRunning
	stateStopping
)

type run struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{}
	wg     sync.WaitGroup
}

type Poller struct {
	state atomic.Uint32
	run   atomic.Pointer[run]

	tasks  []task
	logger *slog.Logger

	mu sync.Mutex
}

func New(logger *slog.Logger) *Poller {
	if logger == nil {
		logger = slog.Default()
	}

	p := &Poller{
		tasks:  make([]task, 0),
		logger: logger,
	}

	return p
}

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
		return fmt.Errorf("task has empty name")
	}

	p.tasks = append(p.tasks, t)

	return nil
}

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
		wg:     sync.WaitGroup{},
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

	ticker := time.NewTicker(t.Interval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := t.Run(ctx)
			if err != nil {
				p.logger.ErrorContext(
					ctx,
					"task failed during poll, will be retried again",
					logger.Err(err),
				)
			}
		}

		ticker.Reset(t.Interval())
	}
}
