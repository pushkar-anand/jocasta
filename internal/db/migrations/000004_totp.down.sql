DROP TABLE IF EXISTS user_recovery_codes;
ALTER TABLE users
    DROP COLUMN totp_confirmed_at;
ALTER TABLE users
    DROP COLUMN totp_enabled;
ALTER TABLE users
    DROP COLUMN totp_secret;
