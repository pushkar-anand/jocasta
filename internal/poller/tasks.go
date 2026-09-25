package poller

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
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
	Name() string
	Interval() time.Duration
	Run(ctx context.Context) error

	// DueIn reports how long to wait before the first run, so that a restart
	// resumes the schedule. Zero runs now, which
	// is the right answer for a task with nothing to resume from.
	//
	// It returns no error because what a task cannot tell about its own history
	// is the task's to interpret: only it knows whether a missing record means
	// the work has never run or that the record could not be read. The poller
	// clamps the answer to between zero and one interval, so a stored time that
	// is wrong in either direction cannot strand the task.
	DueIn(ctx context.Context) time.Duration
}

// dueIn is DueIn for a task that resumes from its last successful scan of
// kind: due interval after that scan, or now when there has been none.
//
// A store that cannot be read waits a full interval. Not knowing whether the
// work is due is a reason to hold off: running anyway would turn a restart
// loop into a scan loop, the one failure the stored schedule exists to
// prevent.
func dueIn(
	ctx context.Context,
	store *inventory.Store,
	kind dbtype.ScanKind,
	interval time.Duration,
	log *slog.Logger,
) time.Duration {
	at, err := store.LastSuccessfulScanAt(ctx, kind)

	switch {
	case errors.Is(err, inventory.ErrNotFound):
		return 0
	case err != nil:
		log.ErrorContext(ctx, "could not tell when the last scan ran, holding off for one interval",
			slog.String("kind", string(kind)),
			logger.Err(err),
		)

		return interval
	}

	return interval - time.Since(at)
}
