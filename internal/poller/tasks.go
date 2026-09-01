package poller

import (
	"context"
	"time"
)

type task interface {
	Name() string
	Interval() time.Duration
	Run(ctx context.Context) error
}
