package catalog

// Canvas schema and workspace interaction validation.
import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// validateCanvasCountForms applies the same project copy contract to canvas
// texts: every base containing {count} declares `<key>_one` with the same
// placeholders. Text whose wording does not change repeats the same text.
func validateCanvasCountForms(texts map[string]string) error {
	for key, value := range texts {
		if strings.HasSuffix(key, "_one") || !strings.Contains(value, "{count}") {
			continue
		}
		one, exists := texts[key+"_one"]
		if !exists || strings.TrimSpace(one) == "" {
			return fmt.Errorf("presentation.text 的计数文案 %q 缺少单数形式 %q", key, key+"_one")
		}
		if !strings.Contains(one, "{count}") {
			return fmt.Errorf("presentation.text 的单数文案 %q 缺少 {count}", key+"_one")
		}
		if !samePlaceholders(value, one) {
			return fmt.Errorf("presentation.text 的 %q 与 %q 占位符不一致", key, key+"_one")
		}
	}
	for key, value := range texts {
		if !strings.HasSuffix(key, "_one") || !strings.Contains(value, "{count}") {
			continue
		}
		base := strings.TrimSuffix(key, "_one")
		general, exists := texts[base]
		if !exists || !strings.Contains(general, "{count}") {
			return fmt.Errorf("presentation.text 的单数文案 %q 缺少含 {count} 的一般形式 %q", key, base)
		}
	}
	return nil
}

func validateCanvas(schema CanvasSchema, runtime RuntimeSchema, content ContentSchema) error {
	fields := map[string]string{
		"id":                 schema.API.Fields.ID,
		"created":            schema.API.Fields.Created,
		"user_id":            schema.API.Fields.UserID,
		"name":               schema.API.Fields.Name,
		"session_id":         schema.API.Fields.SessionID,
		"viewport":           schema.API.Fields.Viewport,
		"bg_style":           schema.API.Fields.BGStyle,
		"created_at":         schema.API.Fields.CreatedAt,
		"updated_at":         schema.API.Fields.UpdatedAt,
		"nodes":              schema.API.Fields.Nodes,
		"count":              schema.API.Fields.Count,
		"node_count":         schema.API.Fields.NodeCount,
		"ok":                 schema.API.Fields.OK,
		"error":              schema.API.Fields.Error,
		"project_id":         schema.API.Fields.ProjectID,
		"type":               schema.API.Fields.Type,
		"x":                  schema.API.Fields.X,
		"y":                  schema.API.Fields.Y,
		"width":              schema.API.Fields.Width,
		"height":             schema.API.Fields.Height,
		"z_index":            schema.API.Fields.ZIndex,
		"data":               schema.API.Fields.Data,
		"visible":            schema.API.Fields.Visible,
		"locked":             schema.API.Fields.Locked,
		"revision":           schema.API.Fields.Revision,
		"asset_id":           schema.API.Fields.AssetID,
		"url":                schema.API.Fields.URL,
		"image_url":          schema.API.Fields.ImageURL,
		"placed_results":     schema.API.Fields.PlacedResults,
		"canvas_width":       schema.API.Fields.CanvasWidth,
		"canvas_height":      schema.API.Fields.CanvasHeight,
		"coordinate_version": schema.API.Fields.CoordinateVersion,
	}
	seenFields := make(map[string]string, len(fields))
	for name, field := range fields {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("api.fields.%s 不能为空", name)
		}
		if previous, exists := seenFields[field]; exists {
			return fmt.Errorf(
				"api.fields.%s 与 %s 重复为 %q",
				previous,
				name,
				field,
			)
		}
		seenFields[field] = name
	}
	pathParameter := strings.TrimSpace(schema.API.PathParameters.ProjectID)
	if pathParameter == "" ||
		strings.ContainsAny(pathParameter, "{} /?#") {
		return errors.New("api.path_parameters.project_id 无效")
	}
	defaults := schema.Defaults
	for name, value := range map[string]string{
		"project_name":          defaults.ProjectName,
		"viewport":              defaults.Viewport,
		"bg_style":              defaults.BGStyle,
		"project_id_prefix":     defaults.ProjectIDPrefix,
		"node_id_prefix":        defaults.NodeIDPrefix,
		"node_data":             defaults.NodeData,
		"merge_filename_prefix": defaults.MergeFilenamePrefix,
		"merge_extension":       defaults.MergeExtension,
		"merge_media_type":      defaults.MergeMediaType,
		"merge_asset_type":      defaults.MergeAssetType,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("defaults.%s 不能为空", name)
		}
	}
	if !json.Valid([]byte(defaults.Viewport)) ||
		!json.Valid([]byte(defaults.NodeData)) {
		return errors.New("defaults.viewport 和 node_data 必须是合法 JSON")
	}
	if defaults.NodeWidth <= 0 || defaults.NodeHeight <= 0 {
		return errors.New("defaults 节点宽高必须大于 0")
	}
	if schema.Limits.MaximumMergeNodes <= 0 ||
		schema.Limits.MaximumBitmapNodes <= 0 ||
		schema.Limits.MaximumDimension <= 0 ||
		schema.Limits.MaximumPlacedResults <= 0 ||
		schema.Limits.MaximumPlacedResultKeyCharacters <= 0 ||
		schema.Limits.MinimumDimension <= 0 ||
		schema.Limits.MinimumDimension > schema.Limits.MaximumDimension {
		return errors.New("limits 必须大于 0，且 minimum_dimension 不超过 maximum_dimension")
	}
	// 坐标契约版本 2 起世界坐标 = 像素（#246）；版本 1 是旧画布，不能作为当前版本。
	if defaults.CoordinateVersion < 2 {
		return errors.New("defaults.coordinate_version 必须是像素坐标契约版本（>= 2）")
	}
	for name, value := range map[string]string{
		"create_action":         schema.Audit.CreateAction,
		"delete_action":         schema.Audit.DeleteAction,
		"merge_action":          schema.Audit.MergeAction,
		"project_resource_type": schema.Audit.ProjectResourceType,
		"merge_resource_type":   schema.Audit.MergeResourceType,
		"detail_fields.name":    schema.Audit.DetailFields.Name,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("audit.%s 不能为空", name)
		}
	}
	frontend := schema.Frontend
	artboard := frontend.Artboard
	if math.IsNaN(artboard.CenterX) || math.IsInf(artboard.CenterX, 0) ||
		math.IsNaN(artboard.CenterY) || math.IsInf(artboard.CenterY, 0) ||
		math.IsNaN(artboard.LegacyMaxEdge) || math.IsInf(artboard.LegacyMaxEdge, 0) || artboard.LegacyMaxEdge <= 0 {
		return errors.New("frontend.artboard 锚点必须有限，旧画板最大边长必须为有限正数")
	}
	validPixels := func(size CanvasPixelSize) bool {
		return size.Width >= schema.Limits.MinimumDimension && size.Width <= schema.Limits.MaximumDimension &&
			size.Height >= schema.Limits.MinimumDimension && size.Height <= schema.Limits.MaximumDimension
	}
	if !validPixels(CanvasPixelSize{Width: artboard.DefaultWidth, Height: artboard.DefaultHeight}) {
		return errors.New("frontend.artboard.default_width/default_height 必须在 limits 的画布尺寸范围内")
	}
	if len(artboard.RatioPixels) == 0 {
		return errors.New("frontend.artboard.ratio_pixels 不能为空")
	}
	ratioPattern := regexp.MustCompile(`^\d+:\d+$`)
	validRatioTable := func(name string, ratios map[string]CanvasPixelSize) error {
		if len(ratios) == 0 {
			return fmt.Errorf("frontend.artboard.%s 不能为空", name)
		}
		for ratio, size := range ratios {
			if !ratioPattern.MatchString(ratio) || !validPixels(size) {
				return fmt.Errorf("frontend.artboard.%s[%q] 必须是整数宽高比和范围内的像素尺寸", name, ratio)
			}
			parts := strings.Split(ratio, ":")
			ratioWidth, _ := strconv.ParseFloat(parts[0], 64)
			ratioHeight, _ := strconv.ParseFloat(parts[1], 64)
			if ratioWidth <= 0 || ratioHeight <= 0 {
				return fmt.Errorf("frontend.artboard.%s[%q] 宽高比必须为正", name, ratio)
			}
			// 像素尺寸须与键的宽高比一致（容许上游取整到 16/32 像素倍数造成的偏差）。
			declared := ratioWidth / ratioHeight
			actual := float64(size.Width) / float64(size.Height)
			if math.Abs(actual-declared)/declared > 0.03 {
				return fmt.Errorf("frontend.artboard.%s[%q] 像素尺寸与宽高比不符", name, ratio)
			}
		}
		return nil
	}
	if err := validRatioTable("ratio_pixels", artboard.RatioPixels); err != nil {
		return err
	}
	for parameter, tiers := range artboard.RatioPixelTiers {
		if strings.TrimSpace(parameter) == "" || len(tiers) == 0 {
			return errors.New("frontend.artboard.ratio_pixel_tiers 须按参数名声明非空档位")
		}
		for tier, ratios := range tiers {
			if strings.TrimSpace(tier) == "" {
				return fmt.Errorf("frontend.artboard.ratio_pixel_tiers.%s 的档位名不能为空", parameter)
			}
			if err := validRatioTable(fmt.Sprintf("ratio_pixel_tiers.%s.%s", parameter, tier), ratios); err != nil {
				return err
			}
		}
	}
	if !(artboard.FrameStrokeWidth > 0) || math.IsInf(artboard.FrameStrokeWidth, 0) ||
		len(artboard.FrameLineDash) == 0 {
		return errors.New("frontend.artboard 虚线边框必须声明正的线宽和虚线段")
	}
	for index, dash := range artboard.FrameLineDash {
		if !(dash > 0) || math.IsInf(dash, 0) {
			return fmt.Errorf("frontend.artboard.frame_line_dash[%d] 必须为有限正数", index)
		}
	}
	label := artboard.Label
	if len(label.CommonRatios) == 0 || !(label.FontSizePixels > 0) || label.OffsetPixels < 0 ||
		!strings.HasPrefix(label.ColorToken, "--") {
		return errors.New("frontend.artboard.label 需要常用比例、正的字号和 CSS 颜色令牌")
	}
	if label.PaddingPixels < 0 || !strings.HasPrefix(label.BackgroundToken, "--") ||
		label.BackgroundOpacity < 0 || label.BackgroundOpacity > 1 {
		return errors.New("frontend.artboard.label 需要非负内边距、CSS 底色令牌和 [0,1] 的底色透明度")
	}
	for _, common := range label.CommonRatios {
		if !regexp.MustCompile(`^[1-9]\d*:[1-9]\d*$`).MatchString(common) {
			return fmt.Errorf("frontend.artboard.label.common_ratios 含无效比例 %q", common)
		}
	}
	if !isHexColor(artboard.OutsideDim.Color) || !(artboard.OutsideDim.Opacity > 0) || artboard.OutsideDim.Opacity > 1 {
		return errors.New("frontend.artboard.outside_dim 需要十六进制颜色和 (0,1] 的透明度")
	}
	transparency := artboard.Transparency
	if !(transparency.CellPixels > 0) || math.IsInf(transparency.CellPixels, 0) ||
		!isHexColor(transparency.LightColor) || !isHexColor(transparency.DarkColor) ||
		!(transparency.Opacity > 0) || transparency.Opacity > 1 {
		return errors.New("frontend.artboard.transparency 需要正的格子边长、两种十六进制颜色和 (0,1] 的透明度")
	}
	// #246：改尺寸锚点只能是九宫格位置之一，默认锚点必须在可选列表里。
	knownAnchors := map[string]bool{
		"top-left": true, "top": true, "top-right": true, "left": true, "center": true,
		"right": true, "bottom-left": true, "bottom": true, "bottom-right": true,
	}
	resize := artboard.Resize
	if len(resize.Anchors) == 0 {
		return errors.New("frontend.artboard.resize.anchors 不能为空")
	}
	offered := make(map[string]bool, len(resize.Anchors))
	for _, anchor := range resize.Anchors {
		if !knownAnchors[anchor] || offered[anchor] {
			return fmt.Errorf("frontend.artboard.resize.anchors 含未知或重复的锚点 %q", anchor)
		}
		offered[anchor] = true
	}
	if !offered[resize.DefaultAnchor] {
		return fmt.Errorf("frontend.artboard.resize.default_anchor %q 不在可选锚点中", resize.DefaultAnchor)
	}
	// #246：初始位图层的填充只能是透明或十六进制颜色。
	if fill := frontend.InitialLayer.Fill; fill != frontend.Export.TransparentBackgroundValue && !isHexColor(fill) {
		return fmt.Errorf("frontend.initial_layer.fill %q 必须是透明值或十六进制颜色", fill)
	}
	if frontend.InitialLayer.MaximumBitmapPixels <= 0 {
		return errors.New("frontend.initial_layer.maximum_bitmap_pixels 必须大于 0")
	}
	brushSize := frontend.Brush
	if !(brushSize.MinimumSize > 0) || brushSize.MaximumSize < brushSize.MinimumSize ||
		brushSize.DefaultSize < brushSize.MinimumSize || brushSize.DefaultSize > brushSize.MaximumSize ||
		math.IsInf(brushSize.MaximumSize, 0) {
		return errors.New("frontend.brush 需要 0 < minimum_size <= default_size <= maximum_size")
	}
	selection := frontend.Selection
	if len(selection.AntsDash) == 0 || selection.AntsTickMilliseconds <= 0 || !isHexColor(selection.TintColor) ||
		!(selection.TintOpacity > 0) || selection.TintOpacity > 1 || selection.PreviewMaxEdge <= 0 {
		return errors.New("frontend.selection 需要蚂蚁线虚线、正的刷新间隔、十六进制色调和预览边长")
	}
	for index, dash := range selection.AntsDash {
		if !(dash > 0) || math.IsInf(dash, 0) {
			return fmt.Errorf("frontend.selection.ants_dash[%d] 必须为有限正数", index)
		}
	}
	mask := frontend.Mask
	if !strings.HasPrefix(mask.TintColorToken, "--") || !(mask.TintOpacity > 0) || mask.TintOpacity > 1 {
		return errors.New("frontend.mask 的色调必须是 CSS 令牌，不透明度在 (0,1]")
	}
	snap := frontend.Snap
	if !(snap.ThresholdPixels > 0) || math.IsInf(snap.ThresholdPixels, 0) ||
		!(snap.GuideStrokeWidth > 0) || math.IsInf(snap.GuideStrokeWidth, 0) ||
		!strings.HasPrefix(snap.GuideColorToken, "--") {
		return errors.New("frontend.snap 阈值、线宽必须为正，颜色必须是 CSS 令牌")
	}
	if frontend.FocusDelayMilliseconds <= 0 ||
		frontend.AutoSaveDelayMilliseconds <= 0 ||
		frontend.SavedIndicatorMilliseconds <= 0 ||
		frontend.ContextMenuBindDelayMilliseconds < 0 ||
		frontend.NodeGapPixels <= 0 ||
		frontend.MinimumNodeSize <= 0 ||
		frontend.MaximumNodeEdge <= 0 ||
		frontend.ModelCaptureLongEdgePixels < 512 ||
		frontend.ModelCaptureLongEdgePixels > 4096 ||
		frontend.ToThreeDLongEdgePixels < 512 ||
		frontend.ToThreeDLongEdgePixels > 4096 ||
		frontend.Viewport.MinimumZoom <= 0 ||
		frontend.Viewport.MaximumZoom < frontend.Viewport.MinimumZoom ||
		frontend.Viewport.DefaultZoom < frontend.Viewport.MinimumZoom ||
		frontend.Viewport.DefaultZoom > frontend.Viewport.MaximumZoom ||
		frontend.Viewport.ZoomStep <= 0 ||
		frontend.Viewport.DecimalPlaces < 0 ||
		frontend.Viewport.DecimalPlaces > 6 ||
		frontend.Viewport.PercentageMultiplier <= 0 ||
		frontend.Viewport.FitPaddingPixels < 0 ||
		!(frontend.Viewport.WheelLineHeightPixels > 0) ||
		frontend.Drawing.BrushIdleCommitMilliseconds <= 0 ||
		!(frontend.Text.LineHeight > 0) || math.IsInf(frontend.Text.LineHeight, 0) ||
		frontend.Drawing.DefaultNodeSize <= 0 ||
		frontend.Drawing.TextNodeWidth <= 0 ||
		frontend.Drawing.TextNodeHeight <= 0 ||
		frontend.Drawing.InitialShapeSize <= 0 ||
		frontend.Drawing.PencilMinimumPoints < 2 ||
		frontend.Drawing.PencilPadding < 0 ||
		frontend.Drawing.MinimumDrawSize <= 0 ||
		frontend.Drawing.LineHeight <= 0 ||
		frontend.Drawing.MinimumCommittedShapeSize <= 0 ||
		frontend.Drawing.SelectionDragThresholdPixels <= 0 ||
		frontend.History.MaximumDepth <= 0 ||
		frontend.History.RecentLimit <= 0 ||
		frontend.History.RecentLimit > frontend.History.MaximumDepth ||
		frontend.History.SnapshotThrottleMilliseconds <= 0 ||
		frontend.Clipboard.OffsetX <= 0 ||
		frontend.Clipboard.OffsetY <= 0 ||
		frontend.RelativeTime.MillisecondsPerMinute <= 0 ||
		frontend.RelativeTime.MinutesPerHour <= 0 ||
		frontend.RelativeTime.HoursPerDay <= 0 ||
		frontend.RelativeTime.DaysPerMonth <= 0 {
		return errors.New("frontend 时间与布局参数必须大于 0")
	}
	if frontend.Drawing.DefaultNodeSize > frontend.MaximumNodeEdge ||
		frontend.MinimumNodeSize > frontend.MaximumNodeEdge {
		return errors.New("frontend 节点默认尺寸与最小尺寸不得超过最大边长")
	}
	export := frontend.Export
	if export.PaddingPixels < 0 ||
		export.Frame.StrokeWidth <= 0 ||
		export.Arrow.HeadLength <= 0 ||
		export.Arrow.HeadAngleDivisor <= 0 ||
		len(export.Frame.LineDash) == 0 {
		return errors.New("frontend.export 数值配置无效")
	}
	for name, color := range map[string]string{
		"default_background_color": export.DefaultBackgroundColor,
		"image_placeholder_color":  export.ImagePlaceholderColor,
		"frame.stroke_color":       export.Frame.StrokeColor,
	} {
		if !isHexColor(color) {
			return fmt.Errorf("frontend.export.%s 颜色无效", name)
		}
	}
	for name, value := range map[string]string{
		"transparent_background_value": export.TransparentBackgroundValue,
		"output_media_type":            export.OutputMediaType,
		"pencil.line_cap":              export.Pencil.LineCap,
		"pencil.line_join":             export.Pencil.LineJoin,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("frontend.export.%s 不能为空", name)
		}
	}
	if !strings.Contains(export.OutputMediaType, "/") {
		return errors.New("frontend.export.output_media_type 无效")
	}
	if export.ArtboardBackgroundColor != export.TransparentBackgroundValue && !isHexColor(export.ArtboardBackgroundColor) {
		return errors.New("frontend.export.artboard_background_color 必须是透明值或 #rrggbb")
	}
	if export.ArtboardBackgroundColor == export.TransparentBackgroundValue && export.OutputMediaType != "image/png" && export.OutputMediaType != "image/webp" {
		return errors.New("frontend.export 透明画板需要支持 alpha 的 output_media_type")
	}
	switch export.Pencil.LineCap {
	case "butt", "round", "square":
	default:
		return errors.New("frontend.export.pencil.line_cap 无效")
	}
	switch export.Pencil.LineJoin {
	case "bevel", "round", "miter":
	default:
		return errors.New("frontend.export.pencil.line_join 无效")
	}
	for index, dash := range export.Frame.LineDash {
		if dash <= 0 {
			return fmt.Errorf(
				"frontend.export.frame.line_dash[%d] 必须大于 0",
				index,
			)
		}
	}
	style := frontend.Style
	if style.DefaultStrokeWidth <= 0 ||
		!isHexColor(style.DefaultStrokeColor) ||
		!isHexColor(style.DefaultShapeStrokeColor) ||
		strings.TrimSpace(style.DefaultFillColor) == "" ||
		!isHexColor(style.DefaultBackgroundColor) ||
		strings.TrimSpace(style.TransparentFillValue) == "" ||
		len(style.PresetColors) == 0 ||
		len(style.StrokeWidths) == 0 ||
		style.RGBMinimum < 0 ||
		style.RGBMaximum <= style.RGBMinimum ||
		strings.TrimSpace(style.SelectionBorder) == "" ||
		strings.TrimSpace(style.SelectionBackground) == "" {
		return errors.New("frontend.style 的默认样式或范围无效")
	}
	for index, color := range style.PresetColors {
		if !isHexColor(color) {
			return fmt.Errorf(
				"frontend.style.preset_colors[%d] 无效: %q",
				index,
				color,
			)
		}
	}
	for index, width := range style.StrokeWidths {
		if width <= 0 {
			return fmt.Errorf(
				"frontend.style.stroke_widths[%d] 必须大于 0",
				index,
			)
		}
	}
	if len(style.FontSizes) == 0 {
		return errors.New("frontend.style.font_sizes 不能为空")
	}
	defaultFontSizeListed := false
	for index, size := range style.FontSizes {
		if !(size > 0) || math.IsInf(size, 0) {
			return fmt.Errorf("frontend.style.font_sizes[%d] 必须为有限正数", index)
		}
		if size == style.DefaultFontSize {
			defaultFontSizeListed = true
		}
	}
	if !defaultFontSizeListed {
		return errors.New("frontend.style.default_font_size 必须出现在 font_sizes 中")
	}
	if len(frontend.Backgrounds) == 0 {
		return errors.New("frontend.backgrounds 不能为空")
	}
	backgroundValues := make(map[string]struct{}, len(frontend.Backgrounds))
	for index, background := range frontend.Backgrounds {
		if strings.TrimSpace(background.Value) == "" ||
			strings.TrimSpace(background.LabelKey) == "" ||
			!isHexColor(background.BackgroundColor) ||
			!isHexColor(background.PatternColor) ||
			background.CellSize <= 0 ||
			background.PatternWidth <= 0 {
			return fmt.Errorf("frontend.backgrounds[%d] 配置无效", index)
		}
		switch background.Pattern {
		case "dots", "grid", "solid":
		default:
			return fmt.Errorf(
				"frontend.backgrounds[%d].pattern 无效: %q",
				index,
				background.Pattern,
			)
		}
		if _, exists := backgroundValues[background.Value]; exists {
			return fmt.Errorf(
				"frontend.backgrounds value 重复: %q",
				background.Value,
			)
		}
		if strings.TrimSpace(
			schema.Presentation.Text[background.LabelKey],
		) == "" {
			return fmt.Errorf(
				"frontend.backgrounds[%d] 引用了未知文本 %q",
				index,
				background.LabelKey,
			)
		}
		backgroundValues[background.Value] = struct{}{}
	}
	if _, exists := backgroundValues[defaults.BGStyle]; !exists {
		return fmt.Errorf(
			"defaults.bg_style 未出现在 frontend.backgrounds: %q",
			defaults.BGStyle,
		)
	}
	keyboard := frontend.Keyboard
	if !(keyboard.SpaceTapMilliseconds > 0) || math.IsInf(keyboard.SpaceTapMilliseconds, 0) {
		return errors.New("frontend.keyboard.space_tap_milliseconds 必须为有限正数")
	}
	for name, value := range map[string]string{
		"space_code":          keyboard.SpaceCode,
		"recenter_key":        keyboard.RecenterKey,
		"undo_key":            keyboard.UndoKey,
		"redo_key":            keyboard.RedoKey,
		"alternate_redo_key":  keyboard.AlternateRedoKey,
		"copy_key":            keyboard.CopyKey,
		"paste_key":           keyboard.PasteKey,
		"select_all_key":      keyboard.SelectAllKey,
		"group_key":           keyboard.GroupKey,
		"layer_forward_key":   keyboard.LayerForwardKey,
		"layer_backward_key":  keyboard.LayerBackwardKey,
		"select_tool_key":     keyboard.SelectToolKey,
		"hand_tool_key":       keyboard.HandToolKey,
		"marquee_tool_key":    keyboard.MarqueeToolKey,
		"lasso_tool_key":      keyboard.LassoToolKey,
		"mask_tool_key":       keyboard.MaskToolKey,
		"crop_tool_key":       keyboard.CropToolKey,
		"brush_tool_key":      keyboard.BrushToolKey,
		"eraser_tool_key":     keyboard.EraserToolKey,
		"text_tool_key":       keyboard.TextToolKey,
		"shape_tool_key":      keyboard.ShapeToolKey,
		"zoom_tool_key":       keyboard.ZoomToolKey,
		"eyedropper_tool_key": keyboard.EyedropperToolKey,
		"brush_size_down_key": keyboard.BrushSizeDownKey,
		"brush_size_up_key":   keyboard.BrushSizeUpKey,
		"swap_colors_key":     keyboard.SwapColorsKey,
		"default_colors_key":  keyboard.DefaultColorsKey,
		"help_key":            keyboard.HelpKey,
		"mask_confirm_key":    keyboard.MaskConfirmKey,
		"mask_exit_key":       keyboard.MaskExitKey,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("frontend.keyboard.%s 不能为空", name)
		}
	}
	for name, value := range map[string]string{
		"layer_forward_key":   keyboard.LayerForwardKey,
		"layer_backward_key":  keyboard.LayerBackwardKey,
		"brush_size_down_key": keyboard.BrushSizeDownKey,
		"brush_size_up_key":   keyboard.BrushSizeUpKey,
	} {
		if utf8.RuneCountInString(value) != 1 {
			return fmt.Errorf("frontend.keyboard.%s 必须是单个字符", name)
		}
	}
	// One bare key per tool (Photoshop): two tools sharing a key would make one of them unreachable.
	toolKeys := map[string]string{}
	for name, value := range map[string]string{
		"select_tool_key":     keyboard.SelectToolKey,
		"hand_tool_key":       keyboard.HandToolKey,
		"marquee_tool_key":    keyboard.MarqueeToolKey,
		"lasso_tool_key":      keyboard.LassoToolKey,
		"mask_tool_key":       keyboard.MaskToolKey,
		"crop_tool_key":       keyboard.CropToolKey,
		"brush_tool_key":      keyboard.BrushToolKey,
		"eraser_tool_key":     keyboard.EraserToolKey,
		"text_tool_key":       keyboard.TextToolKey,
		"shape_tool_key":      keyboard.ShapeToolKey,
		"zoom_tool_key":       keyboard.ZoomToolKey,
		"eyedropper_tool_key": keyboard.EyedropperToolKey,
		"swap_colors_key":     keyboard.SwapColorsKey,
		"default_colors_key":  keyboard.DefaultColorsKey,
	} {
		lower := strings.ToLower(value)
		if other, exists := toolKeys[lower]; exists {
			return fmt.Errorf("frontend.keyboard.%s 与 %s 使用了同一个键 %q", name, other, value)
		}
		toolKeys[lower] = name
	}
	if len(keyboard.ShapeCycle) == 0 {
		return errors.New("frontend.keyboard.shape_cycle 不能为空")
	}
	seenShapes := map[string]bool{}
	for index, shape := range keyboard.ShapeCycle {
		switch shape {
		case "rectangle", "ellipse", "line", "arrow":
		default:
			return fmt.Errorf("frontend.keyboard.shape_cycle[%d] 无效: %q", index, shape)
		}
		if seenShapes[shape] {
			return fmt.Errorf("frontend.keyboard.shape_cycle[%d] 重复: %q", index, shape)
		}
		seenShapes[shape] = true
	}
	if !(keyboard.BrushSizeStepRatio > 0) || keyboard.BrushSizeStepRatio >= 1 {
		return errors.New("frontend.keyboard.brush_size_step_ratio 必须在 (0,1) 之间")
	}
	for name, value := range map[string]string{
		"generate_shortcut":         keyboard.GenerateShortcut,
		"lock_shortcut":             keyboard.LockShortcut,
		"merge_visible_shortcut":    keyboard.MergeVisibleShortcut,
		"cut_shortcut":              keyboard.CutShortcut,
		"duplicate_shortcut":        keyboard.DuplicateShortcut,
		"deselect_shortcut":         keyboard.DeselectShortcut,
		"invert_selection_shortcut": keyboard.InvertSelectionShortcut,
		"zoom_in_shortcut":          keyboard.ZoomInShortcut,
		"zoom_out_shortcut":         keyboard.ZoomOutShortcut,
		"zoom_actual_shortcut":      keyboard.ZoomActualShortcut,
		"zoom_selection_shortcut":   keyboard.ZoomSelectionShortcut,
		"layers_panel_shortcut":     keyboard.LayersPanelShortcut,
		"history_panel_shortcut":    keyboard.HistoryPanelShortcut,
	} {
		if !validShortcut(value) {
			return fmt.Errorf("frontend.keyboard.%s 无效: %q", name, value)
		}
	}
	if keyboard.NudgePixels <= 0 || keyboard.NudgeLargePixels < keyboard.NudgePixels {
		return errors.New("frontend.keyboard.nudge_pixels 必须为正且不大于 nudge_large_pixels")
	}
	if len(keyboard.ReservedShortcuts) == 0 {
		return errors.New("frontend.keyboard.reserved_shortcuts 不能为空")
	}
	for index, value := range keyboard.ReservedShortcuts {
		if !validShortcut(value) {
			return fmt.Errorf("frontend.keyboard.reserved_shortcuts[%d] 无效: %q", index, value)
		}
	}
	if !isHexColor(frontend.Text.DefaultColor) || strings.TrimSpace(frontend.Text.FontFamily) == "" {
		return errors.New("frontend.text 的默认颜色或字体族无效")
	}
	if err := validateStageActions(frontend, content.Messages); err != nil {
		return err
	}
	if err := validateCanvasTools(frontend, content.Messages); err != nil {
		return err
	}
	if err := validateCanvasPanelTabs(frontend, content.Messages); err != nil {
		return err
	}
	if len(keyboard.DeleteKeys) == 0 {
		return errors.New("frontend.keyboard.delete_keys 不能为空")
	}
	for index, key := range keyboard.DeleteKeys {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf(
				"frontend.keyboard.delete_keys[%d] 不能为空",
				index,
			)
		}
	}
	for name, value := range map[string]string{
		"export_filename_fallback": frontend.ExportFilenameFallback,
		"export_extension":         frontend.ExportExtension,
		"upload_task_id":           frontend.UploadTaskID,
		"permission_redirect_path": frontend.PermissionRedirectPath,
		"relative_time.locale":     frontend.RelativeTime.Locale,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("frontend.%s 不能为空", name)
		}
	}
	for name, message := range map[string]string{
		"forbidden":              schema.Presentation.Messages.Forbidden,
		"project_not_found":      schema.Presentation.Messages.ProjectNotFound,
		"nodes_must_array":       schema.Presentation.Messages.NodesMustArray,
		"bitmap_limit_exceeded":  schema.Presentation.Messages.BitmapLimitExceeded,
		"invalid_merge":          schema.Presentation.Messages.InvalidMerge,
		"dimension_limit":        schema.Presentation.Messages.DimensionLimit,
		"merge_failed":           schema.Presentation.Messages.MergeFailed,
		"internal_error":         schema.Presentation.Messages.InternalError,
		"placed_results_invalid": schema.Presentation.Messages.PlacedResultsInvalid,
		"canvas_frame_invalid":   schema.Presentation.Messages.CanvasFrameInvalid,
	} {
		if strings.TrimSpace(message) == "" {
			return fmt.Errorf("presentation.messages.%s 不能为空", name)
		}
	}
	for _, key := range []string{
		"project.list.new",
		"project.list.empty",
		"project.list.node_summary",
		"relative.just_now",
		"relative.minutes_ago",
		"relative.hours_ago",
		"relative.days_ago",
		"task.image_edit",
		"task.image_generate",
		"task.pending",
		"generator.hint",
		"status.generating",
		"error.load_messages",
		"error.create_canvas",
		"error.create_session",
		"error.request_failed",
		"error.sse_interrupted",
		"error.send_failed",
		"error.delete_failed",
		"error.require_session",
		"error.require_selection",
		"error.selection_no_images",
		"error.export_empty",
		"error.export_failed",
		"success.image_sent",
		"status.merging",
		"success.merged_sent",
		"success.exported",
		"permission.denied",
		"save.saving",
		"save.saved",
		"save.failed_retry",
		"empty.title",
		"empty.description_line1",
		"empty.description_line2",
		"panel.generated_files",
		"panel.layers",
		"board.empty",
		"node.generated_alt",
		"node.edit_text_placeholder",
		"node.frame_default_text",
		"node.send_reference",
		"node.reference",
		"toolbar.background",
		"toolbar.background.dots",
		"toolbar.background.grid",
		"toolbar.background.plain",
		"toolbar.background.dark",
		"toolbar.select",
		"toolbar.select_tooltip",
		"toolbar.hand",
		"toolbar.hand_tooltip",
		"toolbar.upload",
		"toolbar.snap",
		"toolbar.snap_on",
		"toolbar.snap_off",
		"toolbar.shapes",
		"toolbar.rectangle",
		"toolbar.arrow",
		"toolbar.line",
		"toolbar.ellipse",
		"toolbar.text",
		"toolbar.pencil",
		"toolbar.generate",
		"toolbar.zoom_out",
		"toolbar.zoom_in",
		"toolbar.reset",
		"toolbar.fit",
		"toolbar.recenter",
		"toolbar.export",
		"chat.default_title",
		"chat.new_board",
		"chat.close",
		"chat.loading",
		"chat.empty",
		"chat.example_one",
		"chat.example_two",
		"files.generated_count",
		"files.uploaded_count",
		"files.empty",
		"files.empty_description",
		"files.uploaded_image",
		"files.ai_image",
		"files.image_alt",
		"files.hidden",
		"files.download",
		"layers.history",
		"layers.empty_history",
		"layers.empty",
		"layers.node.uploaded_image",
		"layers.node.image",
		"layers.node.text",
		"layers.node.frame",
		"layers.node.arrow",
		"layers.node.rectangle",
		"layers.node.generic",
		"layers.unlock",
		"layers.lock",
		"layers.hide",
		"layers.show",
		"context.copy",
		"context.send_to_chat",
		"context.group",
		"context.ungroup",
		"context.paste",
		"context.bring_to_front",
		"context.bring_forward",
		"context.send_backward",
		"context.send_to_back",
		"context.delete",
		"context.unlock",
		"context.lock",
		"context.show",
		"context.hide",
		"context.select_all",
		"shortcut.copy",
		"shortcut.group",
		"shortcut.ungroup",
		"shortcut.paste",
		"shortcut.delete",
		"shortcut.select_all",
		"history.add.image",
		"history.add.text",
		"history.add.frame",
		"history.add.shape",
		"history.add.pencil",
		"history.add.generic",
		"history.delete",
		"history.move",
		"history.style",
		"history.edit_text",
		"history.front",
		"history.back",
		"history.forward",
		"history.backward",
		"history.visibility",
		"history.reorder",
		"history.delete_many",
		"history.paste",
		"history.group",
		"history.ungroup",
		"history.move_many",
		"models.title",
		"models.auto",
		"models.tab.image",
		"models.tab.3d",
		"models.loading",
		"models.empty",
		"models.recommended",
		"style.line_width",
		"style.line_color",
		"style.custom_color",
		"style.fill",
		"style.no_fill",
		"style.rgb",
		"style.confirm",
		"style.cancel",
	} {
		if strings.TrimSpace(schema.Presentation.Text[key]) == "" {
			return fmt.Errorf("presentation.text 缺少 %q", key)
		}
	}
	if err := validateCanvasCountForms(schema.Presentation.Text); err != nil {
		return err
	}
	parameterToken := "{" + pathParameter + "}"
	for name, route := range map[string]string{
		"canvas_project": runtime.HTTP.LegacyRoutes.CanvasProject,
		"canvas_nodes":   runtime.HTTP.LegacyRoutes.CanvasNodes,
	} {
		if !strings.Contains(route, parameterToken) ||
			len(configuredRouteParameters(route)) != 1 {
			return fmt.Errorf(
				"runtime.http.legacy_routes.%s 必须且只能声明参数 %s",
				name,
				parameterToken,
			)
		}
	}
	return nil
}

// Stage-toolbar vocabulary. The enums are the closed set the frontend knows how to place and draw; the cost
// sentence of a button is application.content.messages[stageActionCostMessagePrefix + cost_hint].
var (
	stageActionKinds     = []string{"image", "model", "board"}
	stageActionGroups    = []string{"produce", "output", "view"}
	stageActionCostHints = []string{"approval_card", "process_panel", "free"}
)

const (
	stageActionFreeCostHint      = "free"
	stageActionCostMessagePrefix = "ui.studio.hint.cost."
)

// validateStageActions checks canvas.frontend.stage_actions: the table is non-empty, ids are unique, every
// entry is placed with known enum values, calls_model agrees with the cost hint, names a lucide icon, points
// at existing message texts, and any shortcut names a canvas.frontend.keyboard setting.
func validateStageActions(frontend CanvasFrontendSchema, messages map[string]string) error {
	if len(frontend.StageActions) == 0 {
		return errors.New("frontend.stage_actions 不能为空")
	}
	for _, hint := range stageActionCostHints {
		if strings.TrimSpace(messages[stageActionCostMessagePrefix+hint]) == "" {
			return fmt.Errorf("content.messages 缺少 %q", stageActionCostMessagePrefix+hint)
		}
	}
	presentation := frontend.ProcessingAction
	if err := validateCanvasActionPresentation("frontend.processing_action", presentation.Group, presentation.CostHint, presentation.CallsModel); err != nil {
		return err
	}
	keyboardSettings := keyboardSettingNames(frontend.Keyboard)
	seen := make(map[string]struct{}, len(frontend.StageActions))
	for index, action := range frontend.StageActions {
		path := fmt.Sprintf("frontend.stage_actions[%d]", index)
		if strings.TrimSpace(action.ID) == "" {
			return fmt.Errorf("%s.id 不能为空", path)
		}
		if _, exists := seen[action.ID]; exists {
			return fmt.Errorf("%s.id 重复: %q", path, action.ID)
		}
		seen[action.ID] = struct{}{}
		if len(action.Kinds) == 0 {
			return fmt.Errorf("%s.kinds 不能为空", path)
		}
		for _, kind := range action.Kinds {
			if !containsString(stageActionKinds, kind) {
				return fmt.Errorf("%s.kinds 含无效对象种类 %q", path, kind)
			}
		}
		if err := validateCanvasActionPresentation(path, action.Group, action.CostHint, action.CallsModel); err != nil {
			return err
		}
		if strings.TrimSpace(action.Icon) == "" {
			return fmt.Errorf("%s.icon 不能为空", path)
		}
		for _, reference := range []struct{ name, key string }{
			{"label_key", action.LabelKey},
			{"hint_key", action.HintKey},
			{"target_key", action.TargetKey},
		} {
			if strings.TrimSpace(messages[reference.key]) == "" {
				return fmt.Errorf("%s.%s 引用了未知文本 %q", path, reference.name, reference.key)
			}
		}
		if action.Shortcut != "" {
			if _, exists := keyboardSettings[action.Shortcut]; !exists {
				return fmt.Errorf("%s.shortcut 必须是 frontend.keyboard 中的单键设置名: %q", path, action.Shortcut)
			}
		}
	}
	return nil
}

// This is presentation validation only: neutral process navigation does not promise an execution kind
// or a price. The chosen Offering and the execution kernel remain authoritative for both.
func validateCanvasActionPresentation(path, group, hint string, mark bool) error {
	if !containsString(stageActionGroups, group) {
		return fmt.Errorf("%s.group 无效: %q", path, group)
	}
	if !containsString(stageActionCostHints, hint) {
		return fmt.Errorf("%s.cost_hint 无效: %q", path, hint)
	}
	if hint == "approval_card" && !mark || hint == stageActionFreeCostHint && mark {
		return fmt.Errorf("%s.calls_model 必须与 cost_hint 一致（approval_card 需要标记，free 不允许标记）", path)
	}
	return nil
}

// Stage-rail vocabulary. The rails are the two left-hand strips of the stage; each has its own sections, and
// a thin line separates sections in the order they first appear in the table. The rail chrome texts are the
// expand / collapse toggle every rail ends with.
var (
	toolRailSections = map[string][]string{
		"canvas": {"select", "draw", "snap", "history"},
		"viewer": {"views"},
	}
	toolModifiers      = []string{"mod", "shift", "alt"}
	toolRailChromeText = []string{
		"ui.studio.rail.expand",
		"ui.studio.rail.collapse",
		"ui.studio.rail.expand_hint",
		"ui.studio.rail.collapse_hint",
	}
)

// validateCanvasTools checks canvas.frontend.tools: the table is non-empty, ids are unique within a rail, the
// rail is known and the section belongs to it, the icon is named, the label and hint texts exist, a shortcut
// names a canvas.frontend.keyboard setting that holds a single key, and modifiers are known and need a shortcut.
// Rail tools never call a model, so there is no cost field to check.
func validateCanvasTools(frontend CanvasFrontendSchema, messages map[string]string) error {
	if len(frontend.Tools) == 0 {
		return errors.New("frontend.tools 不能为空")
	}
	for _, key := range toolRailChromeText {
		if strings.TrimSpace(messages[key]) == "" {
			return fmt.Errorf("content.messages 缺少 %q", key)
		}
	}
	keyboardSettings := keyboardSettingNames(frontend.Keyboard)
	seen := make(map[string]struct{}, len(frontend.Tools))
	for index, tool := range frontend.Tools {
		path := fmt.Sprintf("frontend.tools[%d]", index)
		if strings.TrimSpace(tool.ID) == "" {
			return fmt.Errorf("%s.id 不能为空", path)
		}
		sections, knownRail := toolRailSections[tool.Rail]
		if !knownRail {
			return fmt.Errorf("%s.rail 无效: %q", path, tool.Rail)
		}
		identity := tool.Rail + "/" + tool.ID
		if _, exists := seen[identity]; exists {
			return fmt.Errorf("%s.id 在 rail %q 内重复: %q", path, tool.Rail, tool.ID)
		}
		seen[identity] = struct{}{}
		if !containsString(sections, tool.Section) {
			return fmt.Errorf("%s.section 对 rail %q 无效: %q", path, tool.Rail, tool.Section)
		}
		if strings.TrimSpace(tool.Icon) == "" {
			return fmt.Errorf("%s.icon 不能为空", path)
		}
		for _, reference := range []struct{ name, key string }{
			{"label_key", tool.LabelKey},
			{"hint_key", tool.HintKey},
		} {
			if strings.TrimSpace(messages[reference.key]) == "" {
				return fmt.Errorf("%s.%s 引用了未知文本 %q", path, reference.name, reference.key)
			}
		}
		if tool.Shortcut != "" {
			if _, exists := keyboardSettings[tool.Shortcut]; !exists {
				return fmt.Errorf("%s.shortcut 必须是 frontend.keyboard 中的单键设置名: %q", path, tool.Shortcut)
			}
		}
		if len(tool.Modifiers) > 0 && tool.Shortcut == "" {
			return fmt.Errorf("%s.modifiers 只能与 shortcut 一起声明", path)
		}
		seenModifiers := make(map[string]struct{}, len(tool.Modifiers))
		for _, modifier := range tool.Modifiers {
			if !containsString(toolModifiers, modifier) {
				return fmt.Errorf("%s.modifiers 含无效修饰键 %q", path, modifier)
			}
			if _, exists := seenModifiers[modifier]; exists {
				return fmt.Errorf("%s.modifiers 重复: %q", path, modifier)
			}
			seenModifiers[modifier] = struct{}{}
		}
	}
	return nil
}

// Right-panel vocabulary. A tab id names a body the frontend can draw; a kind is a kind of stage object. The
// frontend needs a tab list for every kind, so all three kinds are declared exactly once.
var (
	panelTabIDs   = []string{"layers", "history", "details", "operations", "process", "formats", "parts"}
	panelKindList = []string{"image", "board", "model"}
)

// validateCanvasPanelTabs checks canvas.frontend.panel_tabs: a positive width; tabs with known, unique ids, a
// named icon, existing label and hint texts and, when given, a shortcut that names a canvas.frontend.keyboard
// setting holding a single key; and exactly one entry per kind whose tabs are declared tabs without repeats,
// whose default is one of its tabs, and whose fallback (if any) is one of its tabs too.
func validateCanvasPanelTabs(frontend CanvasFrontendSchema, messages map[string]string) error {
	panel := frontend.PanelTabs
	if panel.WidthPixels <= 0 {
		return errors.New("frontend.panel_tabs.width_pixels 必须为正数")
	}
	if len(panel.Tabs) == 0 {
		return errors.New("frontend.panel_tabs.tabs 不能为空")
	}
	keyboardSettings := keyboardSettingNames(frontend.Keyboard)
	declared := make(map[string]struct{}, len(panel.Tabs))
	for index, tab := range panel.Tabs {
		path := fmt.Sprintf("frontend.panel_tabs.tabs[%d]", index)
		if strings.TrimSpace(tab.ID) == "" {
			return fmt.Errorf("%s.id 不能为空", path)
		}
		if !containsString(panelTabIDs, tab.ID) {
			return fmt.Errorf("%s.id 无效: %q", path, tab.ID)
		}
		if _, exists := declared[tab.ID]; exists {
			return fmt.Errorf("%s.id 重复: %q", path, tab.ID)
		}
		declared[tab.ID] = struct{}{}
		if strings.TrimSpace(tab.Icon) == "" {
			return fmt.Errorf("%s.icon 不能为空", path)
		}
		for _, reference := range []struct{ name, key string }{
			{"label_key", tab.LabelKey},
			{"hint_key", tab.HintKey},
		} {
			if strings.TrimSpace(messages[reference.key]) == "" {
				return fmt.Errorf("%s.%s 引用了未知文本 %q", path, reference.name, reference.key)
			}
		}
		if tab.Shortcut != "" {
			if _, exists := keyboardSettings[tab.Shortcut]; !exists {
				return fmt.Errorf("%s.shortcut 必须是 frontend.keyboard 中的单键设置名: %q", path, tab.Shortcut)
			}
		}
	}
	if len(panel.Kinds) == 0 {
		return errors.New("frontend.panel_tabs.kinds 不能为空")
	}
	seenKinds := make(map[string]struct{}, len(panel.Kinds))
	for index, kind := range panel.Kinds {
		path := fmt.Sprintf("frontend.panel_tabs.kinds[%d]", index)
		if !containsString(panelKindList, kind.Kind) {
			return fmt.Errorf("%s.kind 无效: %q", path, kind.Kind)
		}
		if _, exists := seenKinds[kind.Kind]; exists {
			return fmt.Errorf("%s.kind 重复: %q", path, kind.Kind)
		}
		seenKinds[kind.Kind] = struct{}{}
		if len(kind.Tabs) == 0 {
			return fmt.Errorf("%s.tabs 不能为空", path)
		}
		listed := make(map[string]struct{}, len(kind.Tabs))
		for _, id := range kind.Tabs {
			if _, exists := declared[id]; !exists {
				return fmt.Errorf("%s.tabs 含未声明的标签 %q", path, id)
			}
			if _, exists := listed[id]; exists {
				return fmt.Errorf("%s.tabs 重复: %q", path, id)
			}
			listed[id] = struct{}{}
		}
		if _, exists := listed[kind.Default]; !exists {
			return fmt.Errorf("%s.default 必须是该种类的标签之一: %q", path, kind.Default)
		}
		if kind.DefaultWhenUnavailable != "" {
			if _, exists := listed[kind.DefaultWhenUnavailable]; !exists {
				return fmt.Errorf("%s.default_when_unavailable 必须是该种类的标签之一: %q", path, kind.DefaultWhenUnavailable)
			}
		}
	}
	for _, kind := range panelKindList {
		if _, exists := seenKinds[kind]; !exists {
			return fmt.Errorf("frontend.panel_tabs.kinds 缺少种类 %q", kind)
		}
	}
	return nil
}

// keyboardSettingNames lists the canvas.frontend.keyboard settings that hold a single key or shortcut
// string (by their JSON names); list and number settings cannot be printed beside a button name.
func keyboardSettingNames(keyboard CanvasKeyboardSchema) map[string]struct{} {
	names := map[string]struct{}{}
	keyboardType := reflect.TypeOf(keyboard)
	for index := 0; index < keyboardType.NumField(); index++ {
		field := keyboardType.Field(index)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name != "" && field.Type.Kind() == reflect.String {
			names[name] = struct{}{}
		}
	}
	return names
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// validShortcut accepts "key" or "modifier+...+key" where a modifier is mod, ctrl, alt, shift or meta.
func validShortcut(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), "+")
	// A "+" key itself is written "mod++": the last part is then empty and the one before is "+" 's slot.
	if len(parts) >= 2 && parts[len(parts)-1] == "" {
		parts = append(parts[:len(parts)-2], "+")
	}
	key := parts[len(parts)-1]
	if key == "" {
		return false
	}
	seen := map[string]bool{}
	for _, modifier := range parts[:len(parts)-1] {
		switch modifier {
		case "mod", "ctrl", "alt", "shift", "meta":
		default:
			return false
		}
		if seen[modifier] {
			return false
		}
		seen[modifier] = true
	}
	return true
}

// validateCanvasRatioCoverage：只给比例的 2D 输出尺寸参数（aspect_ratio 等）必须
// 每个比例选项都能由 frontend.artboard.ratio_pixels 换成像素，画布才有确定尺寸（#246）。
func validateCanvasRatioCoverage(offering schemacontract.Offering, artboard CanvasArtboardSchema) error {
	if offering.Domain != schemacontract.DomainImage {
		return nil
	}
	ratioOption := regexp.MustCompile(`^\d+:\d+$`)
	ratios := []string{}
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != schemacontract.SemanticOutputSize {
			continue
		}
		for _, option := range parameter.Options {
			if !ratioOption.MatchString(option.Value) {
				continue
			}
			ratios = append(ratios, option.Value)
			if _, mapped := artboard.RatioPixels[option.Value]; !mapped {
				return fmt.Errorf(
					"参数 %s 的比例 %q 未在 canvas.frontend.artboard.ratio_pixels 声明像素",
					parameter.Name,
					option.Value,
				)
			}
		}
	}
	// 有分辨率档位参数（如 Gemini image_size）的 Offering：每个档位都要覆盖它的全部比例。
	for _, parameter := range offering.Parameters {
		tiers, declared := artboard.RatioPixelTiers[parameter.Name]
		if !declared || len(ratios) == 0 {
			continue
		}
		for _, option := range parameter.Options {
			table, mapped := tiers[option.Value]
			if !mapped {
				return fmt.Errorf("参数 %s 的档位 %q 未在 canvas.frontend.artboard.ratio_pixel_tiers 声明", parameter.Name, option.Value)
			}
			for _, ratio := range ratios {
				if _, covered := table[ratio]; !covered {
					return fmt.Errorf("canvas.frontend.artboard.ratio_pixel_tiers.%s.%s 缺少比例 %q", parameter.Name, option.Value, ratio)
				}
			}
		}
	}
	return nil
}
