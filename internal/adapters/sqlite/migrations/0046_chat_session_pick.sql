-- The candidate the user picked ("选用") as the session's current object: a JSON
-- record of platform IDs only (kind, asset, task, capture). Empty means no pick.
-- It is re-verified for the owner and session every time it is used.
ALTER TABLE go_chat_sessions ADD COLUMN picked TEXT NOT NULL DEFAULT '';
