package generation

import "context"

// PageOffset saturates an out-of-range page instead of wrapping back to an
// earlier page. The caller still returns the requested page and real total.
func PageOffset(page, pageSize int) int {
	if page <= 1 || pageSize <= 0 {
		return 0
	}
	max := int(^uint(0) >> 1)
	if page-1 > max/pageSize {
		return max
	}
	return (page - 1) * pageSize
}

type TaskProjectionRepository interface {
	GetTaskSummary(context.Context, TaskListScope, TaskID) (Task, error)
	GetTaskVersion(context.Context, TaskListScope, TaskID) (int64, error)
}

// AssetDeliveryFor 报告某持久资产的交付类别（required/background/on-demand）。
// 当前目录回退只在冻结快照 outputs 被读投影裁掉（len==0，0020 摘要投影
// 的设计行为）时发生；快照已有 outputs 但未命中声明 = 真实的"无声明"，
// 不得回退（W1 非阻断发现 1 的 W6 修正：区分"快照被裁剪"与"无输出声明"）。
// 路由/行为不回退到当前目录解释旧任务执行语义。
func (m *Module) AssetDeliveryFor(task Task, role string, variant string) string {
	if len(task.Offering.Outputs) > 0 {
		return AssetDelivery(task.Offering.Outputs, role, variant)
	}
	if current, ok := m.catalog.Get(task.Offering.ID); ok {
		return AssetDelivery(current.Outputs, role, variant)
	}
	return ""
}

func (m *Module) GetSummaryScoped(ctx context.Context, scope TaskListScope, id TaskID) (Task, error) {
	if id == "" || (!scope.AllOwners() && scope.OwnerID() == "") || (scope.AllOwners() && scope.OwnerID() != "") {
		return Task{}, &Error{Code: ErrNotFound, Operation: "get", Detail: "任务不存在"}
	}
	if reader, ok := m.repository.(TaskProjectionRepository); ok {
		task, err := reader.GetTaskSummary(ctx, scope, id)
		if err != nil {
			return Task{}, mapRepositoryError("get", err)
		}
		// The task page judges the upstream job from the summary (#326).
		tasks := []Task{task}
		if err := m.withUpstreamJobFacts(ctx, tasks); err != nil {
			return Task{}, err
		}
		return tasks[0], nil
	}
	return m.GetScoped(ctx, scope, id)
}
func (m *Module) GetVersionScoped(ctx context.Context, scope TaskListScope, id TaskID) (int64, error) {
	if id == "" || (!scope.AllOwners() && scope.OwnerID() == "") || (scope.AllOwners() && scope.OwnerID() != "") {
		return 0, &Error{Code: ErrNotFound, Operation: "get", Detail: "任务不存在"}
	}
	if reader, ok := m.repository.(TaskProjectionRepository); ok {
		version, err := reader.GetTaskVersion(ctx, scope, id)
		if err != nil {
			return 0, mapRepositoryError("get", err)
		}
		return version, nil
	}
	task, err := m.GetScoped(ctx, scope, id)
	return task.Version, err
}
