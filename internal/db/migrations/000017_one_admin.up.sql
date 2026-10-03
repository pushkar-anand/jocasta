-- An instance has one admin, made at setup. A database that already holds more
-- keeps the oldest, and the others become editors, so the index below can be
-- built and startup goes on.
UPDATE users
SET role = 'read_write'
WHERE role = 'admin'
  AND id <> (SELECT MIN(id) FROM users WHERE role = 'admin');

CREATE UNIQUE INDEX users_one_admin ON users (role) WHERE role = 'admin';
