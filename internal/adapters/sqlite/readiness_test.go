package sqlite

import (
	"path/filepath"
	"testing"
)

func TestReadinessRequiresRealDatabaseReadsAndWrites(t *testing.T) {
	for _, fault := range []string{"PRAGMA query_only=ON", "DROP TABLE go_chat_turns", "DROP TABLE generation_tasks"} {
		t.Run(fault, func(t *testing.T) {
			repo, err := Open(filepath.Join(t.TempDir(), "data.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if err := repo.CheckReadiness(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.db.Exec(fault); err != nil {
				t.Fatal(err)
			}
			if err := repo.CheckReadiness(t.Context()); err == nil {
				t.Fatal("unusable database reported ready")
			}
			if fault == "PRAGMA query_only=ON" {
				if _, err := repo.db.Exec("PRAGMA query_only=OFF"); err != nil {
					t.Fatal(err)
				}
				if err := repo.CheckReadiness(t.Context()); err != nil {
					t.Fatalf("did not recover: %v", err)
				}
			}
		})
	}
}
