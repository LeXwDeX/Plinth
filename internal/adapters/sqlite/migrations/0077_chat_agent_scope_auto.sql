-- The Agent scope picker was removed from the workspace (#103 review F1).
-- Sessions that stored 2d or 3d would keep that tool filter forever, with no
-- way to switch back, so the upgrade restores the unrestricted scope. The
-- public API now accepts only auto as well.
UPDATE go_chat_sessions SET agent_scope = 'auto' WHERE agent_scope <> 'auto';
