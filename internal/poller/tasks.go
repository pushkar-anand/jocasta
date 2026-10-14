package poller

import (
	"context"
	"errors"
	"time"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// errNotReady is a task reporting it is blocked on another task's output. The
// poller retries it after notReadyRetry.
var errNotReady = errors.New("task has nothing to work on yet")

// notReadyRetry replaces a task's own interval for the one cycle after it
// returns errNotReady. Short, because whatever it waits on is produced by a
// task that runs far more often than it does.
const notReadyRetry = time.Minute

type task interface {
	// Name identifies the task in logs, and is the key its last run is stored
	// under: renaming a task loses its schedule once.
	Name() string
	Interval() time.Duration
	Run(ctx context.Context) error
}

// History is where the poller keeps when each task last finished a run, so
// that a restart resumes every schedule instead of running everything at once.
type History interface {
	// LastRun returns when task last finished a run, or an error wrapping
	// [inventory.ErrNotFound] when it never has.
	LastRun(ctx context.Context, task string) (time.Time, error)
	// RecordRun stores at as when task last finished a run, and runErr as why
	// it failed, nil when it did not.
	RecordRun(ctx context.Context, task string, at time.Time, runErr error) error
}

var _ History = (*inventory.Store)(nil)
