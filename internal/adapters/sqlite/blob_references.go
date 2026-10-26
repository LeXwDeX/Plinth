package sqlite

import "context"

// BlobReferences returns every persisted CAS key, including claims awaiting
// materialization and tombstoned tasks. Integrity scanning and GC share the
// same read-only reference inventory, so a new reference source cannot be
// checked by one maintenance path and missed by the other.
func BlobReferences(ctx context.Context, databasePath string) (map[string]struct{}, error) {
	records, err := BlobReferenceRecords(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]struct{}, len(records))
	for _, record := range records {
		keys[record.StorageKey] = struct{}{}
	}
	return keys, nil
}
