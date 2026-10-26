package tripo

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func taskDependency(remote string) generation.TaskDependency {
	return generation.TaskDependency{
		TaskID:      generation.TaskID("task-" + remote),
		UpstreamJob: generation.UpstreamJob{ProviderID: "tripo", RemoteID: remote},
	}
}

func TestConversionSourceTaskFollowsDeclaration(t *testing.T) {
	parameter, dependency, err := conversionSourceTask(
		generation.Offering{Parameters: []generation.Parameter{
			{Name: "input_task", Type: "task", Semantic: generation.SemanticSourceTask},
			{Name: "format", Type: "select"},
		}},
		map[string]generation.TaskDependency{"input_task": taskDependency("remote-a")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if parameter.Name != "input_task" {
		t.Fatalf("源任务参数解析错误: %#v", parameter)
	}
	if dependency.UpstreamJob.RemoteID != "remote-a" {
		t.Fatalf("远端任务 ID 解析错误: %#v", dependency)
	}
}

func TestConversionSourceTaskAcceptsLegacyTaskParameter(t *testing.T) {
	_, dependency, err := conversionSourceTask(
		generation.Offering{Parameters: []generation.Parameter{
			{Name: "source_task", Type: "task"},
		}},
		map[string]generation.TaskDependency{"source_task": taskDependency("remote-b")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if dependency.UpstreamJob.RemoteID != "remote-b" {
		t.Fatalf("远端任务 ID 解析错误: %#v", dependency)
	}
}

func TestConversionSourceTaskFailsClosed(t *testing.T) {
	otherSemantic := generation.SemanticQuality
	cases := map[string]struct {
		offering     generation.Offering
		dependencies map[string]generation.TaskDependency
	}{
		"无 task 参数": {
			offering: generation.Offering{Parameters: []generation.Parameter{
				{Name: "format", Type: "select"},
			}},
		},
		"task 参数无依赖": {
			offering: generation.Offering{Parameters: []generation.Parameter{
				{Name: "source_task", Type: "task"},
			}},
		},
		"多个源任务候选": {
			offering: generation.Offering{Parameters: []generation.Parameter{
				{Name: "first_task", Type: "task", Semantic: generation.SemanticSourceTask},
				{Name: "second_task", Type: "task"},
			}},
			dependencies: map[string]generation.TaskDependency{
				"first_task":  taskDependency("remote-c"),
				"second_task": taskDependency("remote-d"),
			},
		},
		"唯一 task 参数是非来源语义": {
			offering: generation.Offering{Parameters: []generation.Parameter{
				{Name: "quality_task", Type: "task", Semantic: otherSemantic},
			}},
			dependencies: map[string]generation.TaskDependency{
				"quality_task": taskDependency("remote-e"),
			},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := conversionSourceTask(testCase.offering, testCase.dependencies); err == nil {
				t.Fatal("歧义或缺失的源任务依赖必须 fail-closed")
			}
		})
	}
}

// TestConversionWireBodyRejectsBesideTaskParameter 锁定 wire 组装与源任务解析
// 同一判据：源任务之外再出现 task 参数时零发送（否则同一 remote ID 会被写进
// 两个 wire 字段）。
func TestConversionWireBodyRejectsBesideTaskParameter(t *testing.T) {
	offering := generation.Offering{Parameters: []generation.Parameter{
		{Name: "source_task", Type: "task", Semantic: generation.SemanticSourceTask, WireName: "input"},
		{Name: "side_task", Type: "task"},
		{Name: "format", Type: "select"},
	}}
	source := offering.Parameters[0]
	if _, err := conversionWireBody(
		offering,
		map[string]any{"source_task": "task-1", "side_task": "task-2", "format": "FBX"},
		source,
		"source-remote-1",
	); err == nil {
		t.Fatal("源任务之外的 task 参数必须 fail-closed")
	}
	body, err := conversionWireBody(
		offering,
		map[string]any{"source_task": "task-1", "format": "FBX"},
		source,
		"source-remote-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if body["input"] != "source-remote-1" || body["format"] != "FBX" || len(body) != 2 {
		t.Fatalf("wire body = %#v", body)
	}
}
