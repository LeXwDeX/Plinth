package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

var _ executiongrant.AccessKeyUsageStore = (*Repository)(nil)

// AssetStepsUsed counts the 3D steps of one asset of the owner whatever the
// owner's mode is. The steps mode only counts under the cap; the external MCP
// status block shows the number in every mode, so the same rule is read here.
func (r *Repository) AssetStepsUsed(ctx context.Context, ownerID, sessionID string, counted []string) (int, error) {
	if ownerID == "" || sessionID == "" {
		return 0, executiongrant.ErrInvalid
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return stepsUsedOnConn(ctx, conn, ownerID, sessionID, counted)
}

// AssetExtraSteps reads the steps the person granted one asset beyond the cap
// (#330 step 5C). A session without a grant, or a foreign session, answers
// zero: the status block then reads the plain cap.
func (r *Repository) AssetExtraSteps(ctx context.Context, ownerID, sessionID string) (int, error) {
	if ownerID == "" || sessionID == "" {
		return 0, executiongrant.ErrInvalid
	}
	var extra int
	err := r.db.QueryRowContext(ctx, `SELECT extra_steps FROM go_chat_sessions WHERE id = ? AND owner_id = ?`,
		sessionID, ownerID).Scan(&extra)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return extra, nil
}

// AssetTitle reads the title of one of the owner's chat sessions.
func (r *Repository) AssetTitle(ctx context.Context, ownerID, sessionID string) (string, bool, error) {
	if ownerID == "" || sessionID == "" {
		return "", false, nil
	}
	var title string
	err := r.db.QueryRowContext(ctx, `SELECT title FROM go_chat_sessions WHERE id = ? AND owner_id = ?`, sessionID, ownerID).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return title, true, nil
}

// AccessKeyStepsTotal counts the owner's 3D steps taken through access keys:
// the Tasks of a counted capability that were or may have been charged and are
// bound to a grant of source access_key, plus the key grants approved but not
// yet bound to a Task. A step another entry took in the same asset is not
// counted here, so the total is what the keys did.
func (r *Repository) AccessKeyStepsTotal(ctx context.Context, ownerID string, counted []string) (int, error) {
	if ownerID == "" {
		return 0, executiongrant.ErrInvalid
	}
	if len(counted) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(counted)), ",")
	args := func() []any {
		values := []any{ownerID}
		for _, capability := range counted {
			values = append(values, capability)
		}
		return values
	}
	var tasks int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_tasks AS task
 WHERE task.owner_id = ? AND task.capability IN (`+placeholders+`)
 AND `+taskMayBeChargedSQL("task")+`
 AND EXISTS (SELECT 1 FROM execution_grants AS bound
   WHERE bound.task_id = task.id AND bound.owner_id = task.owner_id AND bound.source = '`+executiongrant.AccessKey+`')`,
		args()...).Scan(&tasks); err != nil {
		return 0, err
	}
	var held int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_grants
 JOIN generation_prepared_inputs AS prepared
   ON prepared.id = execution_grants.prepared_input_id AND prepared.owner_id = execution_grants.owner_id
 WHERE execution_grants.owner_id = ? AND execution_grants.source = '`+executiongrant.AccessKey+`'
 AND execution_grants.status IN ('approved', 'claimed')
 AND json_extract(prepared.payload_json, '$.capability') IN (`+placeholders+`)
 AND `+turnOwnsStoppedGrantNot, args()...).Scan(&held); err != nil {
		return 0, err
	}
	return tasks + held, nil
}

// AccessKeyAssets lists the owner's MCP assets, the most recently active first,
// each with its used 3D steps (#332). An asset is a chat session the external
// MCP service made for a name (mcp_assets); its activity is the latest grant of
// an access key on it, or its creation when no key asked for a step yet. A
// session of the owner that no key ever reached is not an asset of the service.
func (r *Repository) AccessKeyAssets(ctx context.Context, ownerID string, counted []string, limit int) ([]executiongrant.AccessKeyAsset, bool, error) {
	if ownerID == "" || limit < 1 {
		return nil, false, executiongrant.ErrInvalid
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `SELECT assets.session_id, assets.name, COALESCE(sessions.title, ''),
   COALESCE((SELECT MAX(grants.created_at) FROM execution_grants AS grants
     WHERE grants.owner_id = assets.owner_id AND grants.session_id = assets.session_id
     AND grants.source = '`+executiongrant.AccessKey+`'), assets.created_at) AS last_at
 FROM mcp_assets AS assets
 JOIN go_chat_sessions AS sessions ON sessions.id = assets.session_id AND sessions.owner_id = assets.owner_id
 WHERE assets.owner_id = ?
 ORDER BY last_at DESC, assets.session_id
 LIMIT ?`, ownerID, limit+1)
	if err != nil {
		return nil, false, err
	}
	var assets []executiongrant.AccessKeyAsset
	for rows.Next() {
		var asset executiongrant.AccessKeyAsset
		var last string
		if err := rows.Scan(&asset.SessionID, &asset.Name, &asset.Title, &last); err != nil {
			rows.Close()
			return nil, false, err
		}
		if parsed, err := time.Parse(time.RFC3339Nano, last); err == nil {
			asset.LastActivityAt = parsed.UTC()
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, err
	}
	rows.Close()
	more := len(assets) > limit
	if more {
		assets = assets[:limit]
	}
	for index := range assets {
		used, err := stepsUsedOnConn(ctx, conn, ownerID, assets[index].SessionID, counted)
		if err != nil {
			return nil, false, err
		}
		assets[index].Used = used
	}
	return assets, more, nil
}
