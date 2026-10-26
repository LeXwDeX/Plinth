package canvas

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

var ErrProjectNotFound = errors.New("canvas: project not found")

// ErrRevisionConflict：乐观锁冲突（B15）——另一窗口已先保存。
var ErrRevisionConflict = errors.New("canvas: project revision conflict")
var ErrBitmapLimitExceeded = errors.New("canvas: bitmap node limit exceeded")
var ErrSessionNotFound = errors.New("canvas: session not found")

// ErrInvalidPlacedResults：已放置结果清单含空键、控制字符或超出 Schema 上限。
var ErrInvalidPlacedResults = errors.New("canvas: invalid placed results")

// ErrInvalidCanvasFrame：画布像素尺寸超出 Schema 范围，或坐标契约版本不是当前版本（#246）。
var ErrInvalidCanvasFrame = errors.New("canvas: invalid canvas frame")

// ErrCanvasFrameRequired：画布已有像素尺寸时，不带尺寸的保存被拒绝（#246）。这类请求来自
// 仍按旧 512 单位写节点的页面，按 revision 冲突处理，让页面重新加载。
var ErrCanvasFrameRequired = errors.New("canvas: canvas frame required")

type Repository interface {
	ListProjects(context.Context, string) ([]ProjectSummary, error)
	CreateProject(context.Context, Project) (Project, error)
	GetProject(context.Context, string, string) (Project, bool, error)
	GetProjectBySession(context.Context, string, string) (Project, bool, error)
	UpdateProject(context.Context, Project) (bool, error)
	DeleteProjectAndSession(context.Context, string, string) (bool, error)
	// ReplaceNodes 在同一事务和 revision 递增中替换节点；placedResults、frame 为 nil
	// 时保留已存储的已放置结果清单与画布尺寸，非 nil 时一并覆盖。
	ReplaceNodes(context.Context, string, string, int64, []Node, *[]string, *Frame, int) (bool, error)
}

type SessionService interface {
	CreateSession(
		context.Context,
		string,
		string,
		string,
	) (chat.Session, error)
	RenameSession(
		context.Context,
		string,
		string,
		string,
	) (chat.Session, error)
	DeleteSession(context.Context, string, string) error
	OwnsSession(context.Context, string, string) (bool, error)
}

type Project struct {
	ID          string
	OwnerID     string
	Name        string
	SessionID   string
	OwnsSession bool
	Viewport    string
	BGStyle     string
	Nodes       []Node
	// PlacedResults 是已自动加入过画板的任务结果键（#239/#262），保证结果只自动
	// 加入一次、用户删除后刷新不复现。PlacedResultsKnown=false 表示旧画板尚未
	// 初始化（存储为 NULL），此时 PlacedResults 为 nil；已初始化时为非 nil 切片。
	PlacedResults      []string
	PlacedResultsKnown bool
	// Frame 是画布像素尺寸与坐标契约版本（#246）；nil 表示尚未确定尺寸。
	Frame *Frame
	// CoordinateVersion 是节点几何所用的坐标契约版本：新建项目即写入当前版本（尺寸
	// 由前端首次保存时确定）；0 表示旧画布（存储为 NULL），由前端打开时一次性迁移。
	CoordinateVersion int
	// Revision 乐观锁版本（B15/C2）：读取时返回，保存时校验。
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Frame 是画布的像素尺寸（#246）：世界坐标 1 单位 = 1 像素，画布以 Schema 的
// 画板锚点为中心。CoordinateVersion 标明节点几何所用的坐标契约版本。
type Frame struct {
	Width             int
	Height            int
	CoordinateVersion int
}

type ProjectSummary struct {
	ID        string
	Name      string
	SessionID string
	BGStyle   string
	NodeCount int
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Node struct {
	ID        string
	ProjectID string
	Type      string
	X         float64
	Y         float64
	Width     float64
	Height    float64
	ZIndex    int
	Data      string
	Visible   bool
	Locked    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Config struct {
	Repository         Repository
	Sessions           SessionService
	Clock              func() time.Time
	NewID              func(string) string
	DefaultName        string
	DefaultViewport    string
	DefaultBGStyle     string
	ProjectIDPrefix    string
	MaximumBitmapNodes int
	// MaximumPlacedResults 与 MaximumPlacedResultKeyCharacters 来自 Product Schema
	// canvas.limits，约束已放置结果清单。
	MaximumPlacedResults             int
	MaximumPlacedResultKeyCharacters int
	// MinimumFrameDimension、MaximumFrameDimension 与 CoordinateVersion 来自
	// Product Schema canvas.limits / canvas.defaults，约束画布像素尺寸（#246）。
	MinimumFrameDimension int
	MaximumFrameDimension int
	CoordinateVersion     int
	NodeDefaults          Node
}

type UpdateProjectCommand struct {
	Name     *string
	Viewport *string
	BGStyle  *string
}

type NodeInput struct {
	ID      string
	Type    string
	X       *float64
	Y       *float64
	Width   *float64
	Height  *float64
	ZIndex  *int
	Data    *string
	Visible *bool
	Locked  *bool
}

type Service struct {
	repository         Repository
	sessions           SessionService
	clock              func() time.Time
	newID              func(string) string
	defaultName        string
	defaultViewport    string
	defaultBGStyle     string
	projectIDPrefix    string
	maximumBitmapNodes int
	maximumPlaced      int
	maximumPlacedKey   int
	minimumFrame       int
	maximumFrame       int
	coordinateVersion  int
	nodeDefaults       Node
}

func New(config Config) (*Service, error) {
	if config.Repository == nil || config.Sessions == nil {
		return nil, errors.New("canvas: incomplete dependencies")
	}
	for name, value := range map[string]string{
		"default name":      config.DefaultName,
		"default viewport":  config.DefaultViewport,
		"default bg style":  config.DefaultBGStyle,
		"project id prefix": config.ProjectIDPrefix,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("canvas: %s is required", name)
		}
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.NewID == nil {
		config.NewID = randomID
	}
	if config.NodeDefaults.Width <= 0 ||
		config.NodeDefaults.Height <= 0 ||
		strings.TrimSpace(config.NodeDefaults.Data) == "" {
		return nil, errors.New("canvas: invalid node defaults")
	}
	if config.MaximumBitmapNodes <= 0 {
		return nil, errors.New("canvas: maximum bitmap nodes must be positive")
	}
	if config.MaximumPlacedResults <= 0 || config.MaximumPlacedResultKeyCharacters <= 0 {
		return nil, errors.New("canvas: placed results limits must be positive")
	}
	if config.MinimumFrameDimension <= 0 ||
		config.MaximumFrameDimension < config.MinimumFrameDimension ||
		config.CoordinateVersion <= 0 {
		return nil, errors.New("canvas: invalid canvas frame limits")
	}
	return &Service{
		repository:         config.Repository,
		sessions:           config.Sessions,
		clock:              config.Clock,
		newID:              config.NewID,
		defaultName:        config.DefaultName,
		defaultViewport:    config.DefaultViewport,
		defaultBGStyle:     config.DefaultBGStyle,
		projectIDPrefix:    config.ProjectIDPrefix,
		maximumBitmapNodes: config.MaximumBitmapNodes,
		maximumPlaced:      config.MaximumPlacedResults,
		maximumPlacedKey:   config.MaximumPlacedResultKeyCharacters,
		minimumFrame:       config.MinimumFrameDimension,
		maximumFrame:       config.MaximumFrameDimension,
		coordinateVersion:  config.CoordinateVersion,
		nodeDefaults:       config.NodeDefaults,
	}, nil
}

func (service *Service) ListProjects(
	ctx context.Context,
	ownerID string,
) ([]ProjectSummary, error) {
	return service.repository.ListProjects(ctx, ownerID)
}

func (service *Service) CreateProject(
	ctx context.Context,
	ownerID string,
	ownerName string,
	name string,
) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = service.defaultName
	}
	session, err := service.sessions.CreateSession(
		ctx,
		ownerID,
		ownerName,
		name,
	)
	if err != nil {
		return Project{}, fmt.Errorf("canvas: create linked session: %w", err)
	}
	now := service.clock().UTC()
	project, err := service.repository.CreateProject(ctx, Project{
		ID:          service.newID(service.projectIDPrefix),
		OwnerID:     ownerID,
		Name:        name,
		SessionID:   session.ID,
		OwnsSession: true,
		Viewport:    service.defaultViewport,
		BGStyle:     service.defaultBGStyle,
		Nodes:       []Node{},
		// 新项目的节点从一开始就是像素坐标（#246），不能被当作旧画布再迁移。
		CoordinateVersion: service.coordinateVersion,
		CreatedAt:         now,
		UpdatedAt:         now,
	})
	if err == nil {
		return project, nil
	}
	cleanupErr := service.sessions.DeleteSession(ctx, ownerID, session.ID)
	if cleanupErr != nil {
		return Project{}, errors.Join(
			fmt.Errorf("canvas: create project: %w", err),
			fmt.Errorf("canvas: rollback linked session: %w", cleanupErr),
		)
	}
	return Project{}, fmt.Errorf("canvas: create project: %w", err)
}

// GetOrCreateProjectForSession attaches a canvas to an existing creative task.
// The canvas never owns that chat session, so deleting or renaming the canvas
// cannot delete or rename the user's task. The returned bool is true only when
// this call creates the project; an existing or concurrently created project
// returns false.
func (service *Service) GetOrCreateProjectForSession(
	ctx context.Context,
	ownerID string,
	sessionID string,
	name string,
) (Project, bool, error) {
	owned, err := service.sessions.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return Project{}, false, fmt.Errorf("canvas: verify session: %w", err)
	}
	if !owned {
		return Project{}, false, ErrSessionNotFound
	}
	project, found, err := service.repository.GetProjectBySession(ctx, ownerID, sessionID)
	if err != nil {
		return Project{}, false, fmt.Errorf("canvas: load session project: %w", err)
	}
	if found {
		return project, false, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = service.defaultName
	}
	now := service.clock().UTC()
	project, err = service.repository.CreateProject(ctx, Project{
		ID: service.newID(service.projectIDPrefix), OwnerID: ownerID,
		Name: name, SessionID: sessionID, OwnsSession: false,
		Viewport: service.defaultViewport, BGStyle: service.defaultBGStyle,
		Nodes: []Node{}, CoordinateVersion: service.coordinateVersion, CreatedAt: now, UpdatedAt: now,
	})
	if err == nil {
		return project, true, nil
	}
	// A concurrent request may have created the one-to-one project first.
	if existing, exists, lookupErr := service.repository.GetProjectBySession(ctx, ownerID, sessionID); lookupErr == nil && exists {
		return existing, false, nil
	}
	return Project{}, false, fmt.Errorf("canvas: create session project: %w", err)
}

func (service *Service) GetProject(
	ctx context.Context,
	ownerID string,
	projectID string,
) (Project, error) {
	project, found, err := service.repository.GetProject(
		ctx,
		ownerID,
		projectID,
	)
	if err != nil {
		return Project{}, err
	}
	if !found {
		return Project{}, ErrProjectNotFound
	}
	if project.Nodes == nil {
		project.Nodes = []Node{}
	}
	return project, nil
}

func (service *Service) UpdateProject(
	ctx context.Context,
	ownerID string,
	projectID string,
	command UpdateProjectCommand,
) (Project, error) {
	project, err := service.GetProject(ctx, ownerID, projectID)
	if err != nil {
		return Project{}, err
	}
	if command.Name != nil {
		project.Name = strings.TrimSpace(*command.Name)
		if project.Name == "" {
			project.Name = service.defaultName
		}
	}
	if command.Viewport != nil {
		project.Viewport = *command.Viewport
	}
	if command.BGStyle != nil {
		project.BGStyle = *command.BGStyle
	}
	project.UpdatedAt = service.clock().UTC()
	updated, err := service.repository.UpdateProject(ctx, project)
	if err != nil {
		return Project{}, fmt.Errorf("canvas: update project: %w", err)
	}
	if !updated {
		return Project{}, ErrProjectNotFound
	}
	if command.Name != nil && project.SessionID != "" && project.OwnsSession {
		// 与旧实现一致：项目更新是主事务，关联会话标题同步失败不回滚项目。
		_, _ = service.sessions.RenameSession(
			ctx,
			ownerID,
			project.SessionID,
			project.Name,
		)
	}
	return project, nil
}

func (service *Service) DeleteProject(
	ctx context.Context,
	ownerID string,
	projectID string,
) error {
	deleted, err := service.repository.DeleteProjectAndSession(
		ctx,
		ownerID,
		projectID,
	)
	if err != nil {
		return fmt.Errorf("canvas: delete project: %w", err)
	}
	if !deleted {
		return ErrProjectNotFound
	}
	return nil
}

func (service *Service) ReplaceNodes(
	ctx context.Context,
	ownerID string,
	projectID string,
	expectedRevision int64,
	inputs []NodeInput,
	placedResults *[]string,
	frame *Frame,
) (int, error) {
	// frame 为 nil 表示请求未携带画布尺寸，存储值保持不变（兼容旧客户端）；携带时
	// 必须是 Schema 范围内的整数像素与当前坐标契约版本，旧版本不能回写。
	if frame != nil && !service.validFrame(*frame) {
		return 0, ErrInvalidCanvasFrame
	}
	// placedResults 为 nil 表示请求未携带该字段，存储值保持不变（兼容旧客户端）。
	var placed *[]string
	if placedResults != nil {
		normalized, err := service.normalizePlacedResults(*placedResults)
		if err != nil {
			return 0, err
		}
		placed = &normalized
	}
	now := service.clock().UTC()
	// 保留既有节点的 created_at：全量替换不再把创建时间重置为当前。
	existingCreatedAt := map[string]time.Time{}
	if project, found, err := service.repository.GetProject(ctx, ownerID, projectID); err == nil && found {
		for _, node := range project.Nodes {
			existingCreatedAt[node.ID] = node.CreatedAt
		}
	}
	nodes := make([]Node, 0, len(inputs))
	for _, input := range inputs {
		node := service.nodeDefaults
		node.ID = input.ID
		node.ProjectID = projectID
		node.Type = input.Type
		if createdAt, ok := existingCreatedAt[input.ID]; ok {
			node.CreatedAt = createdAt
		} else {
			node.CreatedAt = now
		}
		node.UpdatedAt = now
		if input.X != nil {
			node.X = *input.X
		}
		if input.Y != nil {
			node.Y = *input.Y
		}
		if input.Width != nil {
			node.Width = *input.Width
		}
		if input.Height != nil {
			node.Height = *input.Height
		}
		if input.ZIndex != nil {
			node.ZIndex = *input.ZIndex
		}
		if input.Data != nil {
			node.Data = *input.Data
		}
		if input.Visible != nil {
			node.Visible = *input.Visible
		}
		if input.Locked != nil {
			node.Locked = *input.Locked
		}
		nodes = append(nodes, node)
	}
	replaced, err := service.repository.ReplaceNodes(
		ctx,
		ownerID,
		projectID,
		expectedRevision,
		nodes,
		placed,
		frame,
		service.maximumBitmapNodes,
	)
	if err != nil {
		return 0, fmt.Errorf("canvas: replace nodes: %w", err)
	}
	if !replaced {
		return 0, ErrProjectNotFound
	}
	return len(nodes), nil
}

func (service *Service) validFrame(frame Frame) bool {
	return frame.CoordinateVersion == service.coordinateVersion &&
		frame.Width >= service.minimumFrame && frame.Width <= service.maximumFrame &&
		frame.Height >= service.minimumFrame && frame.Height <= service.maximumFrame
}

// normalizePlacedResults 校验并按首次出现顺序去重；键是前端定义的不透明字符串，
// 不做 trim。条目上限按去重后的数量计算，键长按字符数计算。
func (service *Service) normalizePlacedResults(keys []string) ([]string, error) {
	normalized := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" ||
			!utf8.ValidString(key) ||
			utf8.RuneCountInString(key) > service.maximumPlacedKey ||
			strings.ContainsFunc(key, unicode.IsControl) {
			return nil, ErrInvalidPlacedResults
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key)
		if len(normalized) > service.maximumPlaced {
			return nil, ErrInvalidPlacedResults
		}
	}
	return normalized, nil
}

func randomID(prefix string) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(fmt.Sprintf("canvas random id: %v", err))
	}
	return strings.TrimSpace(prefix) + "_" + hex.EncodeToString(value[:])
}
