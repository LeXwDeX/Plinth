package app

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #473: the asset-list Task source reads the Task a stored done report names,
// by id. A session can create more terminal Tasks than one page holds, and a
// report the runner cannot find opens another (paid) turn for the item.

type fakeAssetListTaskReader struct {
	tasks    map[generation.TaskID]generation.Task
	page     []generation.Task
	getErr   error
	listErr  error
	listed   bool
	getCalls int
}

func (r *fakeAssetListTaskReader) Get(
	_ context.Context, _ generation.OwnerID, id generation.TaskID,
) (generation.Task, error) {
	r.getCalls++
	if r.getErr != nil {
		return generation.Task{}, r.getErr
	}
	task, found := r.tasks[id]
	if !found {
		return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "get"}
	}
	return task, nil
}

func (r *fakeAssetListTaskReader) List(
	_ context.Context, _ generation.TaskListScope, _ generation.ListQuery,
) (generation.TaskPage, error) {
	r.listed = true
	if r.listErr != nil {
		return generation.TaskPage{}, r.listErr
	}
	return generation.TaskPage{Tasks: r.page}, nil
}

func TestAssetListTaskSourceReadsTheReportedTaskById473(t *testing.T) {
	reported := generation.Task{ID: "task-old", SessionID: "session-1", State: generation.TaskSucceeded}
	newer := generation.Task{ID: "task-new", SessionID: "session-1", State: generation.TaskSucceeded}
	// The page holds only the newest Tasks; the reported one has scrolled out.
	reader := &fakeAssetListTaskReader{
		tasks: map[generation.TaskID]generation.Task{"task-old": reported},
		page:  []generation.Task{newer},
	}
	source := assetListTaskSource(reader)
	tasks, err := source(context.Background(), "owner-a", "session-1", "task-old")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "task-old" {
		t.Fatalf("tasks = %#v, want exactly the reported Task", tasks)
	}
	if reader.listed {
		t.Fatal("the reported Task was not found by id: the page was read instead")
	}
}

func TestAssetListTaskSourceFallsBackToThePage473(t *testing.T) {
	page := []generation.Task{{ID: "task-new", SessionID: "session-1", State: generation.TaskSucceeded}}
	other := generation.Task{ID: "task-other", SessionID: "session-2", State: generation.TaskSucceeded}
	for name, testCase := range map[string]struct {
		taskID  generation.TaskID
		tasks   map[generation.TaskID]generation.Task
		getErr  error
		listErr error
	}{
		"no named Task": {taskID: "", tasks: map[generation.TaskID]generation.Task{}},
		"deleted Task": {
			taskID: "task-gone",
			tasks:  map[generation.TaskID]generation.Task{},
		},
		"another session's Task": {
			taskID: "task-other",
			tasks:  map[generation.TaskID]generation.Task{"task-other": other},
		},
	} {
		t.Run(name, func(t *testing.T) {
			reader := &fakeAssetListTaskReader{tasks: testCase.tasks, page: page, getErr: testCase.getErr, listErr: testCase.listErr}
			tasks, err := assetListTaskSource(reader)(context.Background(), "owner-a", "session-1", testCase.taskID)
			if err != nil {
				t.Fatal(err)
			}
			if len(tasks) != 1 || tasks[0].ID != "task-new" {
				t.Fatalf("tasks = %#v, want the page", tasks)
			}
			if !reader.listed {
				t.Fatal("the page was not read as the fallback")
			}
		})
	}
}

func TestAssetListTaskSourceReportsAReadFailure473(t *testing.T) {
	readErr := errors.New("read failed")
	reader := &fakeAssetListTaskReader{
		tasks:  map[generation.TaskID]generation.Task{"task-old": {ID: "task-old", SessionID: "session-1"}},
		getErr: readErr,
	}
	if _, err := assetListTaskSource(reader)(context.Background(), "owner-a", "session-1", "task-old"); !errors.Is(err, readErr) {
		t.Fatalf("err = %v, want the read failure", err)
	}
	reader = &fakeAssetListTaskReader{listErr: readErr}
	if _, err := assetListTaskSource(reader)(context.Background(), "owner-a", "session-1", ""); !errors.Is(err, readErr) {
		t.Fatalf("err = %v, want the page read failure", err)
	}
}
