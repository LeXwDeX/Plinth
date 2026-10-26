-- Agent scope per chat session: which tools the Agent may use ("2d", "3d" or
-- "auto"). Existing and new sessions start in auto, which keeps every tool.
-- The Product Schema declares the modes and what each one allows.
ALTER TABLE go_chat_sessions ADD COLUMN agent_scope TEXT NOT NULL DEFAULT 'auto';
