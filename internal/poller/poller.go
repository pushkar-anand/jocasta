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

type Poller struct {
	state atomic.Uint32

	tasks  []task
	logger *slog.Logger

	wg sync.WaitGroup

	// Protected by mu to prevent data races during initialization/teardown
	mu        sync.RWMutex
	runCtx    context.Context
	ctxCancel context.CancelCauseFunc

	done chan struct{}
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
	p.runCtx = ctx
	p.ctxCancel = cancel
	p.done = make(chan struct{})
	tasks := p.tasks
	p.state.Store(stateRunning)
	p.mu.Unlock()

	go func() {
		<-ctx.Done()
		p.Stop()
	}()

	for _, t := range tasks {
		p.wg.Go(func() {
			p.runTask(t)
		})
	}

	<-p.done

	return nil
}

func (p *Poller) Stop() {
	p.mu.Lock()

	if p.state.Load() != stateRunning {
		p.mu.Unlock()
		return
	}

	p.state.Store(stateStopping)

	cancel, done := p.ctxCancel, p.done

	p.mu.Unlock()

	if cancel != nil {
		cancel(errors.New("poller stop called"))
	}

	p.wg.Wait()

	p.mu.Lock()
	p.state.Store(stateIdle)
	p.mu.Unlock()

	if done != nil {
		close(done)
	}

	return
}

func (p *Poller) runTask(t task) {
	log := p.logger.With(
		slog.String("task", t.Name()),
		slog.Duration("interval", t.Interval()),
	)

	defer func() {
		err := recover()
		if err != nil {
			log.ErrorContext(p.runCtx, "task panicked", slog.String("panic", fmt.Sprint(err)))
		}
	}()

	ticker := time.NewTicker(t.Interval())
	defer ticker.Stop()

	for {
		select {
		case <-p.runCtx.Done():
			return
		case <-ticker.C:
			err := t.Run(p.runCtx)
			if err != nil {
				p.logger.ErrorContext(
					p.runCtx,
					"task failed during poll, will be retried again",
					logger.Err(err),
				)
			}
		}

		ticker.Reset(t.Interval())
	}
}
