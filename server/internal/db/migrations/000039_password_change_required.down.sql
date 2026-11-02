DROP TABLE IF EXISTS password_change_challenges;

ALTER TABLE users DROP COLUMN must_change_password;
