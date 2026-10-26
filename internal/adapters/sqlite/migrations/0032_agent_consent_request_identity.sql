-- Existing pending approvals must remain answerable after request-bound
-- decisions are deployed. Assign an unguessable identity to legacy rows;
-- a decision still requires the client to GET that exact current identity.
INSERT INTO go_agent_state (owner_id, scope, state_key, value_json, updated_at)
SELECT pending.owner_id, pending.scope, 'consent_request_id',
       '"' || lower(hex(randomblob(16))) || '"', CURRENT_TIMESTAMP
FROM go_agent_state AS pending
WHERE pending.state_key = 'consent_pending'
  AND NOT EXISTS (
    SELECT 1 FROM go_agent_state AS existing
    WHERE existing.owner_id = pending.owner_id
      AND existing.scope = pending.scope
      AND existing.state_key = 'consent_request_id'
  );
