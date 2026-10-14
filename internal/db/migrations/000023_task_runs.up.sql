-- When each poller task last finished a run, keyed by the task's name. A
-- restart resumes each task's schedule from here, so no other table's
-- timestamps decide when work is due.
CREATE TABLE task_runs
(
    name        TEXT PRIMARY KEY,
    finished_at TEXT NOT NULL
);
