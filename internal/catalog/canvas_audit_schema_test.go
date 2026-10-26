package catalog

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultCatalogOwnsCanvasAuditContract(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	audit := taskCatalog.Application().Canvas.Audit
	if audit.CreateAction != "create_task" ||
		audit.DeleteAction != "delete_task" ||
		audit.MergeAction != "upload_file" ||
		audit.ProjectResourceType != "canvas_project" ||
		audit.MergeResourceType != "canvas_merge" ||
		audit.DetailFields.Name != "name" {
		t.Fatalf("canvas audit = %+v", audit)
	}
}

func TestCanvasAuditContractIsRequired(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Canvas.Audit.CreateAction = ""
	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("accepted canvas without Schema-driven audit action")
	}
}

func TestCanvasFrameDefaultTextIsRequired(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Canvas.Presentation.Text["node.frame_default_text"] = ""
	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("accepted canvas without Schema-driven frame display text")
	}
}

func TestCanvasExportConfigurationIsRequired(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Canvas.Frontend.Export.OutputMediaType = ""
	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("accepted canvas without Schema-driven export media type")
	}
}

func TestCanvasExportLineDashIsDefensivelyCloned(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := taskCatalog.Application()
	first.Canvas.Frontend.Export.Frame.LineDash[0] = 99
	second := taskCatalog.Application()
	if second.Canvas.Frontend.Export.Frame.LineDash[0] == 99 {
		t.Fatal("canvas export line dash leaked through Application clone")
	}
}

func TestCanvasArtboardConfiguration(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	defaultBoard := application.Canvas.Frontend.Artboard
	// #246: a new canvas without a 2D size is 2048x2048 pixels; legacy canvases migrate from 512 units.
	if defaultBoard.DefaultWidth != 2048 || defaultBoard.DefaultHeight != 2048 || defaultBoard.LegacyMaxEdge != 512 ||
		application.Canvas.Defaults.CoordinateVersion != 2 || application.Canvas.Limits.MinimumDimension != 64 {
		t.Fatalf("canvas pixel defaults = %#v %#v", defaultBoard, application.Canvas.Defaults)
	}
	if defaultBoard.RatioPixels["16:9"] != (CanvasPixelSize{Width: 1344, Height: 768}) {
		t.Fatalf("ratio pixels = %#v", defaultBoard.RatioPixels)
	}
	if !reflect.DeepEqual(taskCatalog.Application().Public().Canvas.Frontend.Artboard, defaultBoard) {
		t.Fatal("public canvas projection changed the delivery frame configuration")
	}
	application.Canvas.Frontend.Artboard.RatioPixels["1:1"] = CanvasPixelSize{Width: 1, Height: 1}
	application.Canvas.Frontend.Artboard.FrameLineDash[0] = 99
	if again := taskCatalog.Application().Canvas.Frontend.Artboard; again.RatioPixels["1:1"].Width != 1024 || again.FrameLineDash[0] == 99 {
		t.Fatal("canvas artboard configuration leaked through Application clone")
	}
	for _, test := range []struct {
		name   string
		change func(*CanvasSchema)
	}{
		{"zero legacy edge", func(canvas *CanvasSchema) { canvas.Frontend.Artboard.LegacyMaxEdge = 0 }},
		{"negative legacy edge", func(canvas *CanvasSchema) { canvas.Frontend.Artboard.LegacyMaxEdge = -1 }},
		{"infinite legacy edge", func(canvas *CanvasSchema) { canvas.Frontend.Artboard.LegacyMaxEdge = math.Inf(1) }},
		{"NaN legacy edge", func(canvas *CanvasSchema) { canvas.Frontend.Artboard.LegacyMaxEdge = math.NaN() }},
		{"infinite horizontal anchor", func(canvas *CanvasSchema) { canvas.Frontend.Artboard.CenterX = math.Inf(-1) }},
		{"NaN vertical anchor", func(canvas *CanvasSchema) { canvas.Frontend.Artboard.CenterY = math.NaN() }},
		{"default below minimum", func(canvas *CanvasSchema) { canvas.Frontend.Artboard.DefaultWidth = 63 }},
		{"default above maximum", func(canvas *CanvasSchema) { canvas.Frontend.Artboard.DefaultHeight = 8193 }},
		{"no ratio table", func(canvas *CanvasSchema) { canvas.Frontend.Artboard.RatioPixels = nil }},
		{"ratio key not a ratio", func(canvas *CanvasSchema) {
			canvas.Frontend.Artboard.RatioPixels = map[string]CanvasPixelSize{"auto": {Width: 1024, Height: 1024}}
		}},
		{"ratio pixels off ratio", func(canvas *CanvasSchema) {
			canvas.Frontend.Artboard.RatioPixels = map[string]CanvasPixelSize{"16:9": {Width: 1024, Height: 1024}}
		}},
		{"ratio pixels outside limits", func(canvas *CanvasSchema) {
			canvas.Frontend.Artboard.RatioPixels = map[string]CanvasPixelSize{"1:1": {Width: 9000, Height: 9000}}
		}},
		{"legacy coordinate version", func(canvas *CanvasSchema) { canvas.Defaults.CoordinateVersion = 1 }},
		{"minimum above maximum", func(canvas *CanvasSchema) { canvas.Limits.MinimumDimension = canvas.Limits.MaximumDimension + 1 }},
		{"zero minimum", func(canvas *CanvasSchema) { canvas.Limits.MinimumDimension = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			application := taskCatalog.Application()
			test.change(&application.Canvas)
			if err := ValidateApplicationSchema(application); err == nil {
				t.Fatal("accepted invalid canvas pixel configuration")
			}
		})
	}
	t.Run("finite world origin", func(t *testing.T) {
		application := taskCatalog.Application()
		application.Canvas.Frontend.Artboard.CenterX = -30
		application.Canvas.Frontend.Artboard.CenterY = 0
		if err := ValidateApplicationSchema(application); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCanvasRatioOnlyOfferingsHavePixelsForEveryRatio(t *testing.T) {
	document, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`            "21:9": {
              "width": 1536,
              "height": 672
            }`)
	mutated := bytes.Replace(document, append([]byte(",\n"), original...), nil, 1)
	if bytes.Equal(mutated, document) {
		t.Fatal("default catalog ratio_pixels 21:9 entry not found")
	}
	if _, err := Load(bytes.NewReader(mutated)); err == nil || !strings.Contains(err.Error(), "21:9") {
		t.Fatalf("an aspect ratio without canvas pixels was accepted: %v", err)
	}
}

func TestCanvasTextSizesAndLayerKeysAreSchemaDeclaredAndValidated(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	frontend := taskCatalog.Application().Canvas.Frontend
	if !reflect.DeepEqual(frontend.Style.FontSizes, []float64{16, 24, 32, 48, 64}) ||
		frontend.Style.DefaultFontSize != 24 ||
		frontend.Keyboard.LayerForwardKey != "]" || frontend.Keyboard.LayerBackwardKey != "[" {
		t.Fatalf("frontend = %+v %+v", frontend.Style, frontend.Keyboard)
	}
	mutations := map[string]func(*ApplicationSchema){
		"empty sizes":        func(a *ApplicationSchema) { a.Canvas.Frontend.Style.FontSizes = nil },
		"non-positive size":  func(a *ApplicationSchema) { a.Canvas.Frontend.Style.FontSizes[0] = 0 },
		"default not listed": func(a *ApplicationSchema) { a.Canvas.Frontend.Style.DefaultFontSize = 25 },
		"empty forward key":  func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.LayerForwardKey = "" },
		"long backward key":  func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.LayerBackwardKey = "[[" },
	}
	for name, mutate := range mutations {
		application := taskCatalog.Application()
		mutate(&application)
		if err := ValidateApplicationSchema(application); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestCanvasShortcutsTextAndNudgeAreSchemaDeclaredAndValidated(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	keyboard := taskCatalog.Application().Canvas.Frontend.Keyboard
	if keyboard.LockShortcut != "mod+/" || keyboard.MergeVisibleShortcut != "mod+alt+shift+e" ||
		keyboard.NudgePixels != 1 || keyboard.NudgeLargePixels != 10 || len(keyboard.ReservedShortcuts) == 0 {
		t.Fatalf("keyboard = %+v", keyboard)
	}
	for value, want := range map[string]bool{
		"mod+/": true, "f7": true, "mod++": true, "mod+alt+shift+e": true,
		"": false, "hyper+a": false, "mod+mod+a": false,
	} {
		if got := validShortcut(value); got != want {
			t.Errorf("validShortcut(%q) = %v, want %v", value, got, want)
		}
	}
	mutations := map[string]func(*ApplicationSchema){
		"bad lock shortcut":     func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.LockShortcut = "hyper+x" },
		"bad merge shortcut":    func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.MergeVisibleShortcut = "" },
		"no reserved shortcuts": func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.ReservedShortcuts = nil },
		"zero nudge":            func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.NudgePixels = 0 },
		"large below small":     func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.NudgeLargePixels = 0.5 },
		"empty text family":     func(a *ApplicationSchema) { a.Canvas.Frontend.Text.FontFamily = "" },
		"bad text colour":       func(a *ApplicationSchema) { a.Canvas.Frontend.Text.DefaultColor = "red" },
	}
	for name, mutate := range mutations {
		application := taskCatalog.Application()
		mutate(&application)
		if err := ValidateApplicationSchema(application); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestCanvasPhotoshopKeysPanelsAndSelectionAreSchemaDeclaredAndValidated(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	frontend := taskCatalog.Application().Canvas.Frontend
	keyboard := frontend.Keyboard
	if keyboard.SelectToolKey != "v" || keyboard.MarqueeToolKey != "m" || keyboard.LassoToolKey != "l" ||
		keyboard.MaskToolKey != "q" || keyboard.CropToolKey != "c" || keyboard.BrushToolKey != "b" ||
		keyboard.ShapeToolKey != "u" || keyboard.ZoomToolKey != "z" || keyboard.EyedropperToolKey != "i" ||
		keyboard.SwapColorsKey != "x" || keyboard.DefaultColorsKey != "d" ||
		keyboard.GenerateShortcut != "mod+enter" || keyboard.DuplicateShortcut != "mod+j" ||
		keyboard.LayersPanelShortcut != "f7" || keyboard.HistoryPanelShortcut != "alt+f9" ||
		!reflect.DeepEqual(keyboard.ShapeCycle, []string{"rectangle", "ellipse", "line", "arrow"}) {
		t.Fatalf("keyboard = %+v", keyboard)
	}
	if frontend.PanelTabs.WidthPixels != 300 ||
		frontend.Brush.MinimumSize != 1 || frontend.Brush.MaximumSize != 200 ||
		frontend.Artboard.OutsideDim.Opacity <= 0 || len(frontend.Selection.AntsDash) == 0 {
		t.Fatalf("frontend = %+v %+v %+v", frontend.PanelTabs, frontend.Brush, frontend.Artboard.OutsideDim)
	}
	// #310: the board mask keeps its own tint (the pre-#310 single-stroke look: the --info token at 0.75), so it
	// stays clearly readable and is not the lighter marquee/lasso selection tint.
	if frontend.Mask.TintColorToken != "--info" || frontend.Mask.TintOpacity != 0.75 {
		t.Fatalf("mask = %+v", frontend.Mask)
	}
	mutations := map[string]func(*ApplicationSchema){
		"mask tint not a token":  func(a *ApplicationSchema) { a.Canvas.Frontend.Mask.TintColorToken = "#38bdf8" },
		"mask tint fully clear":  func(a *ApplicationSchema) { a.Canvas.Frontend.Mask.TintOpacity = 0 },
		"mask tint over one":     func(a *ApplicationSchema) { a.Canvas.Frontend.Mask.TintOpacity = 1.5 },
		"two tools on one key":   func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.LassoToolKey = "M" },
		"swap key on a tool key": func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.SwapColorsKey = "b" },
		"empty zoom tool key":    func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.ZoomToolKey = "" },
		"empty shape cycle":      func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.ShapeCycle = nil },
		"unknown shape":          func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.ShapeCycle = []string{"star"} },
		"repeated shape":         func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.ShapeCycle = []string{"line", "line"} },
		"step ratio too large":   func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.BrushSizeStepRatio = 1 },
		"long brush up key":      func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.BrushSizeUpKey = "]]" },
		"bad generate shortcut":  func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.GenerateShortcut = "hyper+enter" },
		"bad history shortcut":   func(a *ApplicationSchema) { a.Canvas.Frontend.Keyboard.HistoryPanelShortcut = "" },
		"dim not a colour":       func(a *ApplicationSchema) { a.Canvas.Frontend.Artboard.OutsideDim.Color = "black" },
		"dim fully clear":        func(a *ApplicationSchema) { a.Canvas.Frontend.Artboard.OutsideDim.Opacity = 0 },
		"brush default too big":  func(a *ApplicationSchema) { a.Canvas.Frontend.Brush.DefaultSize = 500 },
		"no ants dash":           func(a *ApplicationSchema) { a.Canvas.Frontend.Selection.AntsDash = nil },
		"panel width zero":       func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.WidthPixels = 0 },
		"no default background":  func(a *ApplicationSchema) { a.Canvas.Frontend.Style.DefaultBackgroundColor = "" },
	}
	for name, mutate := range mutations {
		application := taskCatalog.Application()
		mutate(&application)
		if err := ValidateApplicationSchema(application); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

// #246: Gemini's resolution tier (image_size 1K/2K/4K) changes its output pixels; every tier must cover every
// ratio the Offering offers, with the official per-tier sizes.
func TestCanvasRatioPixelTiersCoverTieredOfferings(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	tiers := taskCatalog.Application().Canvas.Frontend.Artboard.RatioPixelTiers["image_size"]
	if tiers["4K"]["1:1"] != (CanvasPixelSize{Width: 4096, Height: 4096}) ||
		tiers["2K"]["16:9"] != (CanvasPixelSize{Width: 2752, Height: 1536}) ||
		tiers["1K"]["21:9"] != (CanvasPixelSize{Width: 1584, Height: 672}) {
		t.Fatalf("image_size tiers = %#v", tiers)
	}
	cloned := taskCatalog.Application()
	cloned.Canvas.Frontend.Artboard.RatioPixelTiers["image_size"]["4K"]["1:1"] = CanvasPixelSize{Width: 1, Height: 1}
	if taskCatalog.Application().Canvas.Frontend.Artboard.RatioPixelTiers["image_size"]["4K"]["1:1"].Width != 4096 {
		t.Fatal("ratio pixel tiers leaked through Application clone")
	}
	document, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(document, &raw); err != nil {
		t.Fatal(err)
	}
	mutate := func(change func(tiers map[string]any)) []byte {
		var copy map[string]any
		_ = json.Unmarshal(document, &copy)
		artboard := copy["application"].(map[string]any)["canvas"].(map[string]any)["frontend"].(map[string]any)["artboard"].(map[string]any)
		change(artboard["ratio_pixel_tiers"].(map[string]any)["image_size"].(map[string]any))
		encoded, _ := json.Marshal(copy)
		return encoded
	}
	for name, change := range map[string]func(map[string]any){
		"missing tier":         func(tiers map[string]any) { delete(tiers, "2K") },
		"tier missing a ratio": func(tiers map[string]any) { delete(tiers["4K"].(map[string]any), "21:9") },
		"tier pixels off ratio": func(tiers map[string]any) {
			tiers["1K"].(map[string]any)["16:9"] = map[string]any{"width": 1024, "height": 1024}
		},
		"tier pixels above maximum": func(tiers map[string]any) {
			tiers["4K"].(map[string]any)["1:1"] = map[string]any{"width": 9000, "height": 9000}
		},
	} {
		if _, err := Load(bytes.NewReader(mutate(change))); err == nil {
			t.Fatalf("%s: invalid ratio_pixel_tiers accepted", name)
		}
	}
}

// #246 phase 2: a new canvas starts with a transparent bitmap layer, and a resize keeps content at a declared anchor.
func TestCanvasInitialLayerAndResizeAnchorAreSchemaDeclared(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	frontend := taskCatalog.Application().Canvas.Frontend
	if !frontend.InitialLayer.Enabled || frontend.InitialLayer.Fill != frontend.Export.TransparentBackgroundValue {
		t.Fatalf("initial layer = %#v", frontend.InitialLayer)
	}
	if frontend.Artboard.Resize.DefaultAnchor != "center" || len(frontend.Artboard.Resize.Anchors) != 9 {
		t.Fatalf("resize = %#v", frontend.Artboard.Resize)
	}
	cloned := taskCatalog.Application()
	cloned.Canvas.Frontend.Artboard.Resize.Anchors[0] = "elsewhere"
	if taskCatalog.Application().Canvas.Frontend.Artboard.Resize.Anchors[0] == "elsewhere" {
		t.Fatal("resize anchors leaked through Application clone")
	}
	for name, change := range map[string]func(*CanvasFrontendSchema){
		"unknown anchor":                      func(f *CanvasFrontendSchema) { f.Artboard.Resize.Anchors = []string{"center", "middle"} },
		"duplicate anchor":                    func(f *CanvasFrontendSchema) { f.Artboard.Resize.Anchors = []string{"center", "center"} },
		"no anchors":                          func(f *CanvasFrontendSchema) { f.Artboard.Resize.Anchors = nil },
		"default not offered":                 func(f *CanvasFrontendSchema) { f.Artboard.Resize.Anchors = []string{"top-left"} },
		"fill neither colour nor transparent": func(f *CanvasFrontendSchema) { f.InitialLayer.Fill = "white" },
		"no bitmap cap":                       func(f *CanvasFrontendSchema) { f.InitialLayer.MaximumBitmapPixels = 0 },
		"no checker cell":                     func(f *CanvasFrontendSchema) { f.Artboard.Transparency.CellPixels = 0 },
		"checker colour not hex":              func(f *CanvasFrontendSchema) { f.Artboard.Transparency.DarkColor = "grey" },
		"checker opacity above one":           func(f *CanvasFrontendSchema) { f.Artboard.Transparency.Opacity = 1.5 },
		"label plate without token":           func(f *CanvasFrontendSchema) { f.Artboard.Label.BackgroundToken = "white" },
		"label plate opacity above one":       func(f *CanvasFrontendSchema) { f.Artboard.Label.BackgroundOpacity = 1.5 },
		"label negative padding":              func(f *CanvasFrontendSchema) { f.Artboard.Label.PaddingPixels = -1 },
	} {
		application := taskCatalog.Application()
		change(&application.Canvas.Frontend)
		if err := ValidateApplicationSchema(application); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	application := taskCatalog.Application()
	application.Canvas.Frontend.InitialLayer.Fill = "#ffffff"
	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("white initial layer rejected: %v", err)
	}
}
