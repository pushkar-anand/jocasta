package poller

import (
	"context"
	"errors"
	"time"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// errNotReady means that a task waits for the output of a different task. The
// poller tries it again after notReadyRetry.
var errNotReady = errors.New("task has nothing to work on yet")

// notReadyRetry replaces the interval of a task for one cycle after it returns
// errNotReady. It is short, because the task that it waits for runs much more
// often.
const notReadyRetry = time.Minute

type task interface {
	// Name identifies the task in logs. It is also the key of its last run.
	// So a new name loses its schedule one time.
	Name() string
	Interval() time.Duration
	Run(ctx context.Context) error
}

// History is where the poller keeps the time when each task last finished a
// run. So a restart continues every schedule, and does not run everything at
// once.
type History interface {
	// LastRun returns when task last finished a run. If it never did, it
	// returns an error that wraps [inventory.ErrNotFound].
	LastRun(ctx context.Context, task string) (time.Time, error)
	// RecordRun stores at as the time when task last finished a run. It
	// stores runErr as the reason of a failure, or nil.
	RecordRun(ctx context.Context, task string, at time.Time, runErr error) error
}

var _ History = (*inventory.Store)(nil)
