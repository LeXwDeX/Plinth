-- The session's production brief and the explicitly picked 3D model (#103).
-- An empty brief is '', an empty pick is ''. The brief is a JSON object whose
-- fields are declared by application.chat.model3d_usage.brief. The third
-- column lets a generic choice card say what it asks for.
ALTER TABLE go_chat_sessions ADD COLUMN brief TEXT NOT NULL DEFAULT '';
ALTER TABLE go_chat_sessions ADD COLUMN explicit_model_3d TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_choices ADD COLUMN kind TEXT NOT NULL DEFAULT '';
