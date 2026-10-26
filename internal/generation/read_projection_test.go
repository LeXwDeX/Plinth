package generation

import (
	"context"
	"testing"
)

type projectionRepositoryProbe struct {
	Repository
	summaryCalls, versionCalls int
}

func (r *projectionRepositoryProbe) GetTaskSummary(context.Context, TaskListScope, TaskID) (Task, error) {
	r.summaryCalls++
	return Task{ID: "task", Version: 8}, nil
}
func (r *projectionRepositoryProbe) GetTaskVersion(context.Context, TaskListScope, TaskID) (int64, error) {
	r.versionCalls++
	return 8, nil
}
func TestVersionAndPublicDetailUseNarrowRepositoryReads(t *testing.T) {
	repository := &projectionRepositoryProbe{}
	module := &Module{repository: repository}
	if version, err := module.GetVersionScoped(context.Background(), OwnTasks("owner"), "task"); err != nil || version != 8 {
		t.Fatalf("version=%d error=%v", version, err)
	}
	if task, err := module.GetSummaryScoped(context.Background(), AllTasks(), "task"); err != nil || task.Version != 8 {
		t.Fatalf("summary=%+v error=%v", task, err)
	}
	if repository.summaryCalls != 1 || repository.versionCalls != 1 {
		t.Fatalf("reads=%+v", repository)
	}
	if _, err := module.GetVersionScoped(context.Background(), OwnTasks(""), "task"); err == nil || repository.versionCalls != 1 {
		t.Fatal("invalid owner reached repository")
	}
}
