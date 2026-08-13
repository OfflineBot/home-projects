-- Keys for git over SSH.
--
-- A key belongs to the owner who registered it. The server writes them into the
-- git user's authorized_keys with a forced command, so a key can do nothing on
-- the machine except talk to this server about repositories.

CREATE TABLE ssh_keys (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    public_key   text NOT NULL,
    fingerprint  text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);
CREATE UNIQUE INDEX ssh_keys_fingerprint_key ON ssh_keys (fingerprint);
