DROP TABLE IF EXISTS recovery_codes;
DROP TABLE IF EXISTS two_factor_challenges;

ALTER TABLE users DROP COLUMN two_factor_enabled;
ALTER TABLE users DROP COLUMN totp_secret;
