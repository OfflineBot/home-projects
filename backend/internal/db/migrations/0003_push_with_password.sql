-- Pushing with the repository's own password, without an account.
--
-- Off by default: a password that may only read is a different thing from one
-- that may write, and turning the second one on should be a decision.

ALTER TABLE groups ADD COLUMN push_with_password boolean NOT NULL DEFAULT false;
