package poller

import (
	"context"
	"time"
)

type task interface {
	Name() string
	Interval() time.Duration
	Run(ctx context.Context) error

	// DueIn reports how long to wait before the first run, so that a restart
	// resumes the schedule rather than starting a new one. Zero runs now, which
	// is the right answer for a task with nothing to resume from.
	//
	// It returns no error because what a task cannot tell about its own history
	// is the task's to interpret: only it knows whether a missing record means
	// the work has never run or that the record could not be read. The poller
	// clamps the answer to between zero and one interval, so a stored time that
	// is wrong in either direction cannot strand the task.
	DueIn(ctx context.Context) time.Duration
}
