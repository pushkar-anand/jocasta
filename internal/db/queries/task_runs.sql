-- name: LastTaskRun :one
SELECT finished_at
FROM task_runs
WHERE name = ?;

-- name: SetTaskRun :exec
INSERT INTO task_runs (name, finished_at, error)
VALUES (?, ?, ?)
ON CONFLICT (name) DO UPDATE SET finished_at = excluded.finished_at,
                                 error       = excluded.error;
