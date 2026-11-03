-- The structured multiview request a user message was sent with (#65): the
-- three-view group, the model and the exact picture of each view. Regenerating
-- the reply sends it again, so the server binds the same pictures and views.
-- Empty for every other message.
ALTER TABLE go_chat_messages ADD COLUMN multiview TEXT NOT NULL DEFAULT '';
