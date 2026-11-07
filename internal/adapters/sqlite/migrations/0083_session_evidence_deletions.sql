-- 0083: 会话删除的证据闭环（#186）。
--
-- scene_representations / model_captures / render_manifests 是不可变证据，
-- 原先无条件禁止 DELETE，导致含 3D 场景、截图或渲染清单的会话无法删除
-- （scene/capture/manifest 对会话是 ON DELETE RESTRICT）。这里增加一个
-- 受控删除表：只有会话删除事务先写入该会话，证据行才允许删除；UPDATE
-- 仍然无条件禁止。控制行与证据行在同一事务内写入/删除，删除失败整体回滚。

CREATE TABLE IF NOT EXISTS session_evidence_deletions (
    session_id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    created_at TEXT NOT NULL
);

DROP TRIGGER scene_representations_immutable_delete;
CREATE TRIGGER scene_representations_immutable_delete
BEFORE DELETE ON scene_representations
WHEN NOT EXISTS (
    SELECT 1 FROM session_evidence_deletions
    WHERE session_id = (SELECT session_id FROM scene_objects WHERE id = OLD.object_id)
)
BEGIN
    SELECT RAISE(ABORT, 'scene representation is immutable');
END;

DROP TRIGGER model_captures_immutable_delete;
CREATE TRIGGER model_captures_immutable_delete
BEFORE DELETE ON model_captures
WHEN NOT EXISTS (
    SELECT 1 FROM session_evidence_deletions
    WHERE session_id = OLD.session_id
)
BEGIN
    SELECT RAISE(ABORT, 'model captures are immutable');
END;

DROP TRIGGER render_manifests_immutable_delete;
CREATE TRIGGER render_manifests_immutable_delete
BEFORE DELETE ON render_manifests
WHEN NOT EXISTS (
    SELECT 1 FROM session_evidence_deletions
    WHERE session_id = OLD.session_id
)
BEGIN
    SELECT RAISE(ABORT, 'render manifest is immutable');
END;