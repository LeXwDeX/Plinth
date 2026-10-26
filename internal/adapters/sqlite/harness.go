package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/LeXwDeX/Plinth/internal/harness"
)

var _ harness.Store = (*Repository)(nil)

type harnessReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getHarnessSelection(ctx context.Context, db harnessReader, owner, session string) (harness.Selection, error) {
	if owner == "" || session == "" {
		return harness.Selection{}, harness.ErrNotFound
	}
	var revision sql.NullInt64
	var digest, encoded sql.NullString
	err := db.QueryRowContext(ctx, `SELECT h.revision,h.digest,h.selection_json
 FROM go_chat_sessions s LEFT JOIN harness_selections h ON h.owner_id=s.owner_id AND h.session_id=s.id
 WHERE s.owner_id=? AND s.id=?`, owner, session).Scan(&revision, &digest, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return harness.Selection{}, harness.ErrNotFound
	}
	if err != nil {
		return harness.Selection{}, err
	}
	if !revision.Valid {
		return harness.DefaultSelection(owner, session), nil
	}
	return decodeHarnessSelection(owner, session, harness.Binding{Revision: revision.Int64, Digest: digest.String}, encoded.String)
}

func decodeHarnessSelection(owner, session string, binding harness.Binding, encoded string) (harness.Selection, error) {
	var s harness.Selection
	if err := json.Unmarshal([]byte(encoded), &s); err != nil {
		return s, err
	}
	if !binding.Valid() || binding.IsZero() || s.SessionID != session || s.Binding != binding {
		return harness.Selection{}, fmt.Errorf("%w: corrupt stored binding", harness.ErrInvalid)
	}
	s.OwnerID = owner
	normalized, err := harness.NormalizeSelection(s)
	if err != nil {
		return harness.Selection{}, err
	}
	if normalized.Digest != binding.Digest {
		return harness.Selection{}, fmt.Errorf("%w: corrupt stored selection", harness.ErrInvalid)
	}
	return normalized, nil
}

func (r *Repository) GetHarnessSelection(ctx context.Context, owner, session string) (harness.Selection, error) {
	return getHarnessSelection(ctx, r.db, owner, session)
}

func (r *Repository) CompareAndSwapHarnessSelection(ctx context.Context, candidate harness.Selection, expectedRevision int64) (harness.Selection, error) {
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return harness.Selection{}, harness.ErrInvalid
	}
	candidate, err := harness.NormalizeSelection(candidate)
	if err != nil {
		return harness.Selection{}, err
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return harness.Selection{}, err
	}
	defer rollbackGrantConn(conn)
	current, err := getHarnessSelection(ctx, conn, candidate.OwnerID, candidate.SessionID)
	if err != nil {
		return harness.Selection{}, err
	}
	if current.Revision != expectedRevision {
		return harness.Selection{}, harness.ErrConflict
	}
	if current.Digest == candidate.Digest {
		// Nothing changed (#327): saving what is already saved keeps the
		// revision, so pending steps bound to it stay valid and the version
		// history does not grow. A→B→A still makes new revisions.
		return current, nil
	}
	if current.Revision == 0 {
		raw, err := json.Marshal(current)
		if err != nil {
			return harness.Selection{}, err
		}
		if _, err = conn.ExecContext(ctx, `INSERT INTO harness_selection_versions(owner_id,session_id,revision,digest,selection_json) VALUES(?,?,0,?,?)`, current.OwnerID, current.SessionID, current.Digest, string(raw)); err != nil {
			return harness.Selection{}, err
		}
	}
	candidate.Revision = expectedRevision + 1
	candidate.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(candidate)
	if err != nil {
		return harness.Selection{}, err
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO harness_selection_versions(owner_id,session_id,revision,digest,selection_json) VALUES(?,?,?,?,?)`, candidate.OwnerID, candidate.SessionID, candidate.Revision, candidate.Digest, string(raw)); err != nil {
		return harness.Selection{}, err
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO harness_selections(owner_id,session_id,revision,digest,selection_json) VALUES(?,?,?,?,?)
 ON CONFLICT(owner_id,session_id) DO UPDATE SET revision=excluded.revision,digest=excluded.digest,selection_json=excluded.selection_json`, candidate.OwnerID, candidate.SessionID, candidate.Revision, candidate.Digest, string(raw)); err != nil {
		return harness.Selection{}, err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return harness.Selection{}, err
	}
	return candidate, nil
}

func (r *Repository) GetHarnessSelectionVersion(ctx context.Context, owner, session string, binding harness.Binding) (harness.Selection, error) {
	if !binding.Valid() || binding.IsZero() {
		return harness.Selection{}, harness.ErrInvalid
	}
	// Read current state too: an ownership/session or current-table failure must
	// not be hidden by a synthesized default version.
	current, err := getHarnessSelection(ctx, r.db, owner, session)
	if err != nil {
		return harness.Selection{}, err
	}
	var encoded, digest string
	err = r.db.QueryRowContext(ctx, `SELECT selection_json,digest FROM harness_selection_versions WHERE owner_id=? AND session_id=? AND revision=?`, owner, session, binding.Revision).Scan(&encoded, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		if binding.Revision == 0 && current.Revision == 0 && current.Binding == binding {
			return current, nil
		}
		return harness.Selection{}, harness.ErrNotFound
	}
	if err != nil {
		return harness.Selection{}, err
	}
	if digest != binding.Digest {
		return harness.Selection{}, harness.ErrNotFound
	}
	return decodeHarnessSelection(owner, session, binding, encoded)
}

// The caller owns BEGIN IMMEDIATE, serializing admission with selection CAS.
func verifyCurrentHarness(ctx context.Context, conn *sql.Conn, owner, session string, binding harness.Binding) error {
	current, err := getHarnessSelection(ctx, conn, owner, session)
	if err != nil {
		return err
	}
	if binding.IsZero() {
		if current.Revision != 0 {
			return harness.ErrStale
		}
		return nil
	}
	if !binding.Valid() || current.Binding != binding {
		return harness.ErrStale
	}
	return nil
}
