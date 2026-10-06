-- When the token stops working. Null means it never does.
ALTER TABLE api_tokens ADD COLUMN expires_at TEXT;
