ALTER TABLE authentication_users
    ADD COLUMN open_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX authentication_users_open_id_unique
    ON authentication_users(open_id)
    WHERE open_id <> '';
