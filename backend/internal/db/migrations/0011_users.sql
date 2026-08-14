-- More than one person.
--
-- Anyone may ask for an account; nobody gets one until the owner says so. An
-- account that exists but is not approved can sign in to nothing — the refusal
-- happens at the sign-in, not at every page afterwards.
--
-- Everyone who is already here was approved by the act of existing.
ALTER TABLE users
    ADD COLUMN approved boolean NOT NULL DEFAULT true,
    ADD COLUMN approved_at timestamptz,
    ADD COLUMN note text NOT NULL DEFAULT '';
UPDATE users SET approved = true, approved_at = created_at;
