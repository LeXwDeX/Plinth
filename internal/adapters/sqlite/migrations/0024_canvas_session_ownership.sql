-- Existing canvas projects created their own chat session. New Agent
-- workspace canvases attach to an existing session and must never delete it.
ALTER TABLE go_canvas_projects
    ADD COLUMN owns_session INTEGER NOT NULL DEFAULT 1 CHECK (owns_session IN (0, 1));
