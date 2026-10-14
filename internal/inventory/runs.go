package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// LastRun returns when the poller task named task last finished a run. It
// returns ErrNotFound when the task has never finished one.
func (s *Store) LastRun(ctx context.Context, task string) (time.Time, error) {
	at, err := s.q.LastTaskRun(ctx, task)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, fmt.Errorf("last run of %s: %w", task, ErrNotFound)
	case err != nil:
		return time.Time{}, fmt.Errorf("last run of %s: %w", task, err)
	}

	return at.Time, nil
}

// RecordRun stores at as when the poller task named task last finished a run.
func (s *Store) RecordRun(ctx context.Context, task string, at time.Time) error {
	err := s.q.SetTaskRun(ctx, models.SetTaskRunParams{Name: task, FinishedAt: dbtype.NewTime(at)})
	if err != nil {
		return fmt.Errorf("record run of %s: %w", task, err)
	}

	return nil
}
