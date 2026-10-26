package sqlite

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestListSortsIdentifiersBeforeLoadingLargeTaskPayloads(t *testing.T) {
	const (
		childModeEnvironment = "ART_AI_LIST_RLIMIT_CHILD"
		databaseEnvironment  = "ART_AI_LIST_RLIMIT_DATABASE"
	)
	if os.Getenv(childModeEnvironment) == "1" {
		repository, err := Open(os.Getenv(databaseEnvironment))
		if err != nil {
			t.Fatal(err)
		}
		defer repository.Close()
		if _, err := repository.db.Exec(`PRAGMA temp_store = FILE`); err != nil {
			t.Fatal(err)
		}
		var fileSizeLimit syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &fileSizeLimit); err != nil {
			t.Fatal(err)
		}
		fileSizeLimit.Cur = 64 << 10
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &fileSizeLimit); err != nil {
			t.Fatal(err)
		}
		page, err := repository.List(
			context.Background(),
			generation.AllTasks(),
			generation.ListQuery{
				Limit:       9,
				ActiveFirst: true,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Tasks) != 9 {
			t.Fatalf("tasks = %d, want 9", len(page.Tasks))
		}
		return
	}

	root := t.TempDir()
	databasePath := filepath.Join(root, "tasks.db")
	repository, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}

	largeInput := `{"image":"` + strings.Repeat("x", 4<<20) + `"}`
	for index := range 20 {
		createdAt := formatTime(time.Date(
			2026,
			time.July,
			index+1,
			0,
			0,
			0,
			0,
			time.UTC,
		))
		if _, err := repository.db.Exec(`
			INSERT INTO generation_tasks (
				id,
				owner_id,
				capability,
				offering_json,
				input_json,
				request_hash,
				state,
				progress,
				materialize_failures,
				version,
				created_at,
				updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("task-%02d", index),
			"owner",
			generation.Capability("image-to-3d"),
			`{}`,
			largeInput,
			fmt.Sprintf("hash-%02d", index),
			generation.TaskSucceeded,
			100,
			0,
			1,
			createdAt,
			createdAt,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(
		os.Args[0],
		"-test.run=^TestListSortsIdentifiersBeforeLoadingLargeTaskPayloads$",
		"-test.count=1",
	)
	command.Env = append(
		os.Environ(),
		childModeEnvironment+"=1",
		databaseEnvironment+"="+databasePath,
		"SQLITE_TMPDIR="+root,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("limited-temp child failed: %v\n%s", err, output)
	}
}
