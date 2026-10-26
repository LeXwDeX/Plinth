-- Structured attachments of a chat message: asset ID, source Task ID, capture
-- ID, role/kind and the stable handle the Agent uses to name the object.
-- Existing messages keep an empty list.
ALTER TABLE go_chat_messages ADD COLUMN attachments TEXT NOT NULL DEFAULT '[]';
