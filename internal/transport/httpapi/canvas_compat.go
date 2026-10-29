package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (s *Server) registerLegacyCanvasContracts() {
	legacyRoutes := s.application.Runtime.HTTP.LegacyRoutes
	s.mux.HandleFunc(
		"GET "+legacyRoutes.CanvasProjects,
		s.withPrincipal(s.legacyListCanvasProjects),
	)
	s.mux.HandleFunc(
		"POST "+legacyRoutes.CanvasProjects,
		s.withPrincipal(s.legacyCreateCanvasProject),
	)
	s.mux.HandleFunc(
		"GET "+legacyRoutes.CanvasProject,
		s.withPrincipal(s.legacyGetCanvasProject),
	)
	s.mux.HandleFunc(
		"PATCH "+legacyRoutes.CanvasProject,
		s.withPrincipal(s.legacyUpdateCanvasProject),
	)
	s.mux.HandleFunc(
		"DELETE "+legacyRoutes.CanvasProject,
		s.withPrincipal(s.requireCanvasAdmin(s.legacyDeleteCanvasProject)),
	)
	s.mux.HandleFunc(
		"PUT "+legacyRoutes.CanvasNodes,
		s.withPrincipal(s.legacyReplaceCanvasNodes),
	)
	s.mux.HandleFunc(
		"POST "+legacyRoutes.CanvasMerge,
		s.withPrincipal(s.requireCanvasAdmin(s.legacyMergeCanvasImages)),
	)
}

func (s *Server) requireCanvasAdmin(next principalHandler) principalHandler {
	return func(
		writer http.ResponseWriter,
		request *http.Request,
		principal access.Principal,
	) {
		if !principal.IsAdmin() {
			s.writeCanvasError(
				writer,
				http.StatusForbidden,
				s.application.Canvas.Presentation.Messages.Forbidden,
			)
			return
		}
		next(writer, request, principal)
	}
}

func (s *Server) legacyListCanvasProjects(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	projects, err := s.canvas.ListProjects(request.Context(), principal.ID())
	if err != nil {
		s.logger.Error("list canvas projects", "error", err)
		s.writeCanvasError(
			writer,
			http.StatusInternalServerError,
			s.application.Canvas.Presentation.Messages.InternalError,
		)
		return
	}
	payload := make([]map[string]any, 0, len(projects))
	for _, project := range projects {
		payload = append(payload, s.canvasSummaryPayload(project))
	}
	writeJSON(writer, http.StatusOK, payload)
}

func (s *Server) legacyCreateCanvasProject(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	body := s.decodeCanvasObject(writer, request)
	fields := s.application.Canvas.API.Fields
	name, _ := body[fields.Name].(string)
	sessionID, _ := body[fields.SessionID].(string)
	var project canvas.Project
	var created bool
	var err error
	if sessionID != "" {
		project, created, err = s.canvas.GetOrCreateProjectForSession(
			request.Context(), principal.ID(), sessionID, name,
		)
	} else if principal.IsAdmin() {
		project, err = s.canvas.CreateProject(
			request.Context(), principal.ID(), principal.Claims.Name, name,
		)
		created = err == nil
	} else {
		s.writeCanvasError(writer, http.StatusForbidden, s.application.Canvas.Presentation.Messages.Forbidden)
		return
	}
	if errors.Is(err, canvas.ErrSessionNotFound) {
		s.writeCanvasError(writer, http.StatusNotFound, s.application.Canvas.Presentation.Messages.ProjectNotFound)
		return
	}
	if err != nil {
		s.logger.Error("create canvas project", "error", err)
		s.writeCanvasError(
			writer,
			http.StatusInternalServerError,
			s.application.Canvas.Presentation.Messages.InternalError,
		)
		return
	}
	audit := s.application.Canvas.Audit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       audit.CreateAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: audit.ProjectResourceType,
		ResourceID:   project.ID,
		Detail: map[string]any{
			audit.DetailFields.Name: project.Name,
		},
	})
	payload := s.canvasProjectPayload(project, false)
	payload[fields.Created] = created
	writeJSON(writer, http.StatusCreated, payload)
}

func (s *Server) legacyGetCanvasProject(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	pathParameter := s.application.Canvas.API.PathParameters.ProjectID
	project, err := s.canvas.GetProject(
		request.Context(),
		principal.ID(),
		request.PathValue(pathParameter),
	)
	if errors.Is(err, canvas.ErrProjectNotFound) {
		s.writeCanvasError(
			writer,
			http.StatusNotFound,
			s.application.Canvas.Presentation.Messages.ProjectNotFound,
		)
		return
	}
	if err != nil {
		s.logger.Error("get canvas project", "error", err)
		s.writeCanvasError(
			writer,
			http.StatusInternalServerError,
			s.application.Canvas.Presentation.Messages.InternalError,
		)
		return
	}
	writeJSON(writer, http.StatusOK, s.canvasProjectPayload(project, true))
}

func (s *Server) legacyUpdateCanvasProject(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	body := s.decodeCanvasObject(writer, request)
	fields := s.application.Canvas.API.Fields
	pathParameter := s.application.Canvas.API.PathParameters.ProjectID
	project, err := s.canvas.UpdateProject(
		request.Context(),
		principal.ID(),
		request.PathValue(pathParameter),
		canvas.UpdateProjectCommand{
			Name:     canvasStringField(body, fields.Name),
			Viewport: canvasStringField(body, fields.Viewport),
			BGStyle:  canvasStringField(body, fields.BGStyle),
		},
	)
	if errors.Is(err, canvas.ErrProjectNotFound) {
		s.writeCanvasError(
			writer,
			http.StatusNotFound,
			s.application.Canvas.Presentation.Messages.ProjectNotFound,
		)
		return
	}
	if err != nil {
		s.logger.Error("update canvas project", "error", err)
		s.writeCanvasError(
			writer,
			http.StatusInternalServerError,
			s.application.Canvas.Presentation.Messages.InternalError,
		)
		return
	}
	writeJSON(writer, http.StatusOK, s.canvasProjectPayload(project, false))
}

func (s *Server) legacyDeleteCanvasProject(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	pathParameter := s.application.Canvas.API.PathParameters.ProjectID
	projectID := request.PathValue(pathParameter)
	err := s.canvas.DeleteProject(
		request.Context(),
		principal.ID(),
		projectID,
	)
	if errors.Is(err, canvas.ErrProjectNotFound) {
		s.writeCanvasError(
			writer,
			http.StatusNotFound,
			s.application.Canvas.Presentation.Messages.ProjectNotFound,
		)
		return
	}
	if err != nil {
		s.logger.Error("delete canvas project", "error", err)
		s.writeCanvasError(
			writer,
			http.StatusInternalServerError,
			s.application.Canvas.Presentation.Messages.InternalError,
		)
		return
	}
	audit := s.application.Canvas.Audit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       audit.DeleteAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: audit.ProjectResourceType,
		ResourceID:   projectID,
	})
	writeJSON(writer, http.StatusOK, map[string]bool{
		s.application.Canvas.API.Fields.OK: true,
	})
}

func (s *Server) legacyReplaceCanvasNodes(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	body := s.decodeCanvasObject(writer, request)
	fields := s.application.Canvas.API.Fields
	rawNodes, valid := body[fields.Nodes].([]any)
	if !valid {
		s.writeCanvasError(
			writer,
			http.StatusBadRequest,
			s.application.Canvas.Presentation.Messages.NodesMustArray,
		)
		return
	}
	inputs := make([]canvas.NodeInput, 0, len(rawNodes))
	for _, rawNode := range rawNodes {
		node, valid := rawNode.(map[string]any)
		if !valid {
			s.writeCanvasError(
				writer,
				http.StatusBadRequest,
				s.application.Canvas.Presentation.Messages.NodesMustArray,
			)
			return
		}
		inputs = append(inputs, canvas.NodeInput{
			ID:      canvasLegacyRequiredString(node, fields.ID),
			Type:    canvasLegacyRequiredString(node, fields.Type),
			X:       canvasNumberField(node, fields.X),
			Y:       canvasNumberField(node, fields.Y),
			Width:   canvasNumberField(node, fields.Width),
			Height:  canvasNumberField(node, fields.Height),
			ZIndex:  canvasIntegerField(node, fields.ZIndex),
			Data:    canvasStringValueField(node, fields.Data),
			Visible: canvasBooleanField(node, fields.Visible),
			Locked:  canvasBooleanField(node, fields.Locked),
		})
	}
	// #239/#262：已放置结果清单可选；缺省或 null 时保留存储值（旧客户端兼容），
	// 携带时必须是字符串数组，并与节点在同一事务和 revision 中保存。
	placedResults, valid := canvasPlacedResultsField(body, fields.PlacedResults)
	if !valid {
		s.writeCanvasError(
			writer,
			http.StatusBadRequest,
			s.application.Canvas.Presentation.Messages.PlacedResultsInvalid,
		)
		return
	}
	// #246：画布像素尺寸可选；宽、高、坐标契约版本须同时携带（缺省或 null 时保留
	// 存储值，兼容旧客户端），与节点在同一事务和 revision 中保存。
	frame, valid := canvasFrameField(body, fields)
	if !valid {
		s.writeCanvasError(
			writer,
			http.StatusBadRequest,
			s.application.Canvas.Presentation.Messages.CanvasFrameInvalid,
		)
		return
	}
	// B15 乐观锁：客户端带 revision（读取项目时返回的当前值）才校验；
	// 旧客户端不带则跳过，保持兼容。
	expectedRevision := int64(0)
	if raw, exists := body[fields.Revision]; exists {
		if value, ok := raw.(float64); ok && value >= 1 {
			expectedRevision = int64(value)
		}
	}
	pathParameter := s.application.Canvas.API.PathParameters.ProjectID
	count, err := s.canvas.ReplaceNodes(
		request.Context(),
		principal.ID(),
		request.PathValue(pathParameter),
		expectedRevision,
		inputs,
		placedResults,
		frame,
	)
	if errors.Is(err, canvas.ErrInvalidCanvasFrame) {
		s.writeCanvasError(
			writer,
			http.StatusBadRequest,
			s.application.Canvas.Presentation.Messages.CanvasFrameInvalid,
		)
		return
	}
	if errors.Is(err, canvas.ErrInvalidPlacedResults) {
		s.writeCanvasError(
			writer,
			http.StatusBadRequest,
			s.application.Canvas.Presentation.Messages.PlacedResultsInvalid,
		)
		return
	}
	if errors.Is(err, canvas.ErrProjectNotFound) {
		s.writeCanvasError(
			writer,
			http.StatusNotFound,
			s.application.Canvas.Presentation.Messages.ProjectNotFound,
		)
		return
	}
	if errors.Is(err, canvas.ErrRevisionConflict) || errors.Is(err, canvas.ErrCanvasFrameRequired) {
		s.writeCanvasError(
			writer,
			http.StatusConflict,
			s.application.Canvas.Presentation.Messages.RevisionConflict,
		)
		return
	}
	if errors.Is(err, canvas.ErrBitmapLimitExceeded) {
		s.writeCanvasError(
			writer,
			http.StatusBadRequest,
			// The Schema message names the limit with {limit}; the number is the one enforced (#149).
			strings.ReplaceAll(
				s.application.Canvas.Presentation.Messages.BitmapLimitExceeded,
				"{limit}",
				strconv.Itoa(s.application.Canvas.Limits.MaximumBitmapNodes),
			),
		)
		return
	}
	if err != nil {
		s.logger.Error("replace canvas nodes", "error", err)
		s.writeCanvasError(
			writer,
			http.StatusInternalServerError,
			s.application.Canvas.Presentation.Messages.InternalError,
		)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		fields.OK:    true,
		fields.Count: count,
	})
}

func (s *Server) legacyMergeCanvasImages(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	body := s.decodeCanvasObject(writer, request)
	fields := s.application.Canvas.API.Fields
	rawNodes, valid := body[fields.Nodes].([]any)
	if !valid {
		s.writeCanvasError(
			writer,
			http.StatusBadRequest,
			s.application.Canvas.Presentation.Messages.InvalidMerge,
		)
		return
	}
	nodes := make([]canvas.MergeNode, 0, len(rawNodes))
	for _, rawNode := range rawNodes {
		node, valid := rawNode.(map[string]any)
		if !valid {
			s.writeCanvasError(
				writer,
				http.StatusBadRequest,
				s.application.Canvas.Presentation.Messages.InvalidMerge,
			)
			return
		}
		imageURL, imageURLValid := node[fields.ImageURL].(string)
		x, xValid := canvasRequiredNumber(node, fields.X)
		y, yValid := canvasRequiredNumber(node, fields.Y)
		width, widthValid := canvasRequiredNumber(node, fields.Width)
		height, heightValid := canvasRequiredNumber(node, fields.Height)
		if !imageURLValid ||
			!xValid ||
			!yValid ||
			!widthValid ||
			!heightValid {
			s.writeCanvasError(
				writer,
				http.StatusBadRequest,
				s.application.Canvas.Presentation.Messages.InvalidMerge,
			)
			return
		}
		nodes = append(nodes, canvas.MergeNode{
			ImageURL: imageURL,
			X:        x,
			Y:        y,
			Width:    width,
			Height:   height,
		})
	}
	asset, err := s.canvasImages.Merge(
		request.Context(),
		generation.OwnerID(principal.ID()),
		nodes,
	)
	if errors.Is(err, canvas.ErrInvalidMerge) {
		s.writeCanvasError(
			writer,
			http.StatusBadRequest,
			s.application.Canvas.Presentation.Messages.InvalidMerge,
		)
		return
	}
	if errors.Is(err, canvas.ErrDimensionLimit) {
		s.writeCanvasError(
			writer,
			http.StatusBadRequest,
			s.application.Canvas.Presentation.Messages.DimensionLimit,
		)
		return
	}
	if err != nil {
		s.logger.Error("merge canvas images", "error", err)
		s.writeCanvasError(
			writer,
			http.StatusInternalServerError,
			s.application.Canvas.Presentation.Messages.MergeFailed,
		)
		return
	}
	assetURL := renderCanvasAssetURL(
		s.application.Runtime.HTTP.LegacyRoutes.AssetItem,
		string(asset.ID),
	)
	audit := s.application.Canvas.Audit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       audit.MergeAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: audit.MergeResourceType,
		ResourceID:   string(asset.ID),
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		fields.AssetID: asset.ID,
		fields.URL:     assetURL,
	})
}

func canvasStringField(payload map[string]any, field string) *string {
	value, exists := payload[field]
	if !exists {
		return nil
	}
	text, valid := value.(string)
	if !valid {
		return nil
	}
	return &text
}

// canvasPlacedResultsField 只做线协议类型校验：字段缺省或为 null 返回 (nil, true)；
// 非字符串数组返回 (nil, false)。空键、控制字符、上限与去重由画板领域按 Schema 处理。
func canvasPlacedResultsField(payload map[string]any, field string) (*[]string, bool) {
	value, exists := payload[field]
	if !exists || value == nil {
		return nil, true
	}
	items, valid := value.([]any)
	if !valid {
		return nil, false
	}
	keys := make([]string, 0, len(items))
	for _, item := range items {
		key, valid := item.(string)
		if !valid {
			return nil, false
		}
		keys = append(keys, key)
	}
	return &keys, true
}

// canvasFrameField 只做线协议类型校验：三个字段都缺省或为 null 返回 (nil, true)；
// 只给部分字段或值不是整数返回 (nil, false)。尺寸范围与版本由画布领域按 Schema 校验。
func canvasFrameField(payload map[string]any, fields catalog.CanvasAPIFields) (*canvas.Frame, bool) {
	names := []string{fields.CanvasWidth, fields.CanvasHeight, fields.CoordinateVersion}
	values := make([]int, 0, len(names))
	for _, name := range names {
		raw, exists := payload[name]
		if !exists || raw == nil {
			continue
		}
		number, isNumber := raw.(float64)
		if !isNumber || number != math.Trunc(number) || math.Abs(number) > math.MaxInt32 {
			return nil, false
		}
		values = append(values, int(number))
	}
	if len(values) == 0 {
		return nil, true
	}
	if len(values) != len(names) {
		return nil, false
	}
	return &canvas.Frame{Width: values[0], Height: values[1], CoordinateVersion: values[2]}, true
}

func canvasRequiredNumber(
	payload map[string]any,
	field string,
) (float64, bool) {
	value, valid := payload[field].(float64)
	return value, valid
}

func renderCanvasAssetURL(route string, assetID string) string {
	parameters := configuredPathParameters(route)
	if len(parameters) != 1 {
		return route
	}
	return strings.Replace(
		route,
		"{"+parameters[0]+"}",
		url.PathEscape(assetID),
		1,
	)
}

func canvasLegacyRequiredString(payload map[string]any, field string) string {
	value, exists := payload[field]
	if !exists {
		return "undefined"
	}
	return canvasLegacyString(value)
}

func canvasStringValueField(payload map[string]any, field string) *string {
	value, exists := payload[field]
	if !exists || value == nil {
		return nil
	}
	text := canvasLegacyString(value)
	return &text
}

func canvasLegacyString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if item == nil {
				parts = append(parts, "")
				continue
			}
			parts = append(parts, canvasLegacyString(item))
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	default:
		return fmt.Sprint(typed)
	}
}

func canvasNumberField(payload map[string]any, field string) *float64 {
	value, exists := payload[field]
	if !exists || value == nil {
		return nil
	}
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return nil
		}
		number = parsed
	case bool:
		if typed {
			number = 1
		}
	default:
		return nil
	}
	return &number
}

func canvasIntegerField(payload map[string]any, field string) *int {
	number := canvasNumberField(payload, field)
	if number == nil {
		return nil
	}
	value := int(*number)
	return &value
}

func canvasBooleanField(payload map[string]any, field string) *bool {
	value, exists := payload[field]
	if !exists {
		return nil
	}
	boolean, valid := value.(bool)
	if !valid {
		return nil
	}
	return &boolean
}

func (s *Server) decodeCanvasObject(
	writer http.ResponseWriter,
	request *http.Request,
) map[string]any {
	request.Body = http.MaxBytesReader(
		writer,
		request.Body,
		s.application.Runtime.HTTP.JSONBodyMaxBytes,
	)
	payload := map[string]any{}
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&payload); err != nil {
		return map[string]any{}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return map[string]any{}
	}
	return payload
}

func (s *Server) canvasSummaryPayload(
	project canvas.ProjectSummary,
) map[string]any {
	fields := s.application.Canvas.API.Fields
	return map[string]any{
		fields.ID:        project.ID,
		fields.Name:      project.Name,
		fields.SessionID: project.SessionID,
		fields.BGStyle:   project.BGStyle,
		fields.CreatedAt: project.CreatedAt,
		fields.UpdatedAt: project.UpdatedAt,
		fields.NodeCount: map[string]any{
			fields.Nodes: project.NodeCount,
		},
	}
}

func (s *Server) canvasProjectPayload(
	project canvas.Project,
	includeNodes bool,
) map[string]any {
	fields := s.application.Canvas.API.Fields
	payload := map[string]any{
		fields.ID:        project.ID,
		fields.UserID:    project.OwnerID,
		fields.Name:      project.Name,
		fields.SessionID: project.SessionID,
		fields.Viewport:  project.Viewport,
		fields.BGStyle:   project.BGStyle,
		fields.Revision:  project.Revision,
		fields.CreatedAt: project.CreatedAt,
		fields.UpdatedAt: project.UpdatedAt,
	}
	if includeNodes {
		nodes := make([]map[string]any, 0, len(project.Nodes))
		for _, node := range project.Nodes {
			nodes = append(nodes, s.canvasNodePayload(node))
		}
		payload[fields.Nodes] = nodes
		// null = 旧画板尚未初始化已放置结果清单；数组 = 已初始化（可为空）。
		if project.PlacedResultsKnown {
			placed := project.PlacedResults
			if placed == nil {
				placed = []string{}
			}
			payload[fields.PlacedResults] = placed
		} else {
			payload[fields.PlacedResults] = nil
		}
		// #246：尺寸 null + 版本有值 = 新建像素画布尚未定尺寸；版本也为 null = 旧画布，
		// 由前端打开时一次性迁移后保存。
		payload[fields.CanvasWidth] = nil
		payload[fields.CanvasHeight] = nil
		payload[fields.CoordinateVersion] = nil
		if project.CoordinateVersion > 0 {
			payload[fields.CoordinateVersion] = project.CoordinateVersion
		}
		if project.Frame != nil {
			payload[fields.CanvasWidth] = project.Frame.Width
			payload[fields.CanvasHeight] = project.Frame.Height
			payload[fields.CoordinateVersion] = project.Frame.CoordinateVersion
		}
	}
	return payload
}

func (s *Server) canvasNodePayload(node canvas.Node) map[string]any {
	fields := s.application.Canvas.API.Fields
	return map[string]any{
		fields.ID:        node.ID,
		fields.ProjectID: node.ProjectID,
		fields.Type:      node.Type,
		fields.X:         node.X,
		fields.Y:         node.Y,
		fields.Width:     node.Width,
		fields.Height:    node.Height,
		fields.ZIndex:    node.ZIndex,
		fields.Data:      node.Data,
		fields.Visible:   node.Visible,
		fields.Locked:    node.Locked,
		fields.CreatedAt: node.CreatedAt,
		fields.UpdatedAt: node.UpdatedAt,
	}
}

func (s *Server) writeCanvasError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	writeJSON(writer, status, map[string]string{
		s.application.Canvas.API.Fields.Error: message,
	})
}
