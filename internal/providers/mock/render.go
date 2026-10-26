package mock

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// builtModel 是一个作业共用的模型：同一作业的 GLB、OBJ、FBX 等格式是同一份
// 网格，贴图也是同一张。
type builtModel struct {
	spec    modelSpec
	mesh    meshData
	texture []byte
}

func promptOf(input map[string]any) string {
	prompt, _ := input["prompt"].(string)
	return prompt
}

func capabilityLabel(capability generation.Capability) string {
	switch string(capability) {
	case "text-to-image":
		return "T2I"
	case "image-to-image":
		return "I2I"
	case "background-removal":
		return "NO-BG"
	case "":
		return "MOCK"
	}
	return strings.ToUpper(string(capability))
}

// renderPlan 生成一个产物的字节。调用方已持有适配器锁；这里不加锁。
func (a *Adapter) renderPlan(stored *job, plan claimPlan) ([]byte, error) {
	seed := digest("seed", stored.request.SubmissionKey, plan.key)
	switch plan.role {
	case "image":
		if edited, ok, err := renderEdit(stored, plan, seed); err != nil {
			return nil, err
		} else if ok {
			return edited, nil
		}
		return renderRequestImage(stored, plan, seed)
	case "thumbnail", "texture":
		model, err := a.modelFor(stored)
		if err != nil {
			return nil, err
		}
		if plan.role == "texture" {
			return model.texture, nil
		}
		return renderModelThumbnail(model.spec)
	}
	model, err := a.modelFor(stored)
	if err != nil {
		return nil, err
	}
	name := "MockHumanoid"
	fidelity := ""
	switch stored.request.Offering.Capability {
	case generation.Rigging:
		fidelity = "skeleton"
	case generation.Animation:
		fidelity = "animation"
	}
	switch plan.format {
	case "splat":
		return buildSplat(model.mesh, seed), nil
	case "glb":
		// Only the model itself is large; attachments keep their own size.
		var body []byte
		if stored.large && !plan.attachment {
			body, err = buildPaddedGLB(model.mesh, embeddedTexture(stored, model), name, fidelity, LargeModelBytes)
		} else {
			body, err = buildGLB(model.mesh, embeddedTexture(stored, model), name, fidelity)
		}
		if err == nil {
			sum := sha256.Sum256(body)
			a.glbs[hex.EncodeToString(sum[:])] = stored
		}
		return body, err
	case "gltf":
		return buildGLTF(model.mesh, model.texture, name, fidelity)
	case "obj":
		return buildOBJ(model.mesh, model.spec.Label, objMaterialOf(stored.request.Offering)), nil
	case "mtl":
		return buildMTL(), nil
	case "stl":
		return buildSTL(model.mesh), nil
	case "fbx":
		return buildFBX(model.mesh, model.spec.Label, fidelity), nil
	case "usdz":
		return buildUSDZ(embeddedTexture(stored, model))
	case "3mf":
		return build3MF(model.mesh)
	case "blend":
		return buildBlend(), nil
	}
	return nil, fmt.Errorf("mock: 未知产物格式 %q", plan.format)
}

func renderRequestImage(stored *job, plan claimPlan, seed [32]byte) ([]byte, error) {
	width, height := mockImageDimensions(stored.input)
	promptHash := digest("prompt", promptOf(stored.input))
	capability := stored.request.Offering.Capability
	label := fmt.Sprintf("%s %d/%d #%s %dx%d",
		capabilityLabel(capability), plan.index+1, plan.total, hex.EncodeToString(seed[:3]), width, height)
	return renderImagePNG(imageSpec{
		Width:      width,
		Height:     height,
		Seed:       seed,
		PromptHash: promptHash,
		Label:      label,
		Cutout:     string(capability) == "background-removal",
	})
}

// embeddedTexture is the texture a job embeds in its GLB and USDZ: the PNG,
// followed by the job's textureTail (#330 markers).
func embeddedTexture(stored *job, model *builtModel) []byte {
	if len(stored.textureTail) == 0 {
		return model.texture
	}
	return append(append([]byte(nil), model.texture...), stored.textureTail...)
}

// modelFor 按作业构造（并缓存）模型。几何来自来源任务（处理步骤）或本任务，
// 这样重拓扑、换贴图的结果和来源长得像；换贴图换色，重拓扑减面。
func (a *Adapter) modelFor(stored *job) (*builtModel, error) {
	if stored.model != nil {
		return stored.model, nil
	}
	capability := string(stored.request.Offering.Capability)
	geometrySource := string(stored.request.TaskID)
	if source, ok := stored.input["source_task"].(string); ok && source != "" {
		geometrySource = source
	}
	geometrySeed := digest("geometry", geometrySource)
	textureSeed := digest("texture", stored.request.SubmissionKey)
	hueHash := digest("hue", geometrySource)
	if capability == "retexture" {
		hueHash = digest("hue", promptOf(stored.input), stored.request.SubmissionKey)
	}
	hue := float64(uint16(hueHash[0])<<8|uint16(hueHash[1])) / 65535 * 360
	// 图生 3D 的模型随输入图的主色；换贴图随风格图；处理步骤沿用来源任务的颜色。
	if stored.edit != nil && stored.edit.reference != nil {
		if fromImage, ok := dominantHue(stored.edit.reference); ok {
			// 模型躯干的颜色是色相加 180 度，所以先减掉，躯干才和图里的主色一致。
			hue = math.Mod(fromImage+180, 360)
		}
	} else if remembered, ok := a.rememberedHue(geometrySource); ok && capability != "retexture" {
		hue = remembered
	}
	a.rememberHue(string(stored.request.TaskID), hue)
	spec := modelSpec{
		GeometrySeed: geometrySeed,
		TextureSeed:  textureSeed,
		Hue:          hue,
		Subdivision:  8,
		TextureSize:  512,
		Label:        fmt.Sprintf("%s #%s", capabilityLabel(stored.request.Offering.Capability), hex.EncodeToString(geometrySeed[:3])),
	}
	if capability == "remesh" {
		spec.Subdivision = 4
	}
	texture, err := renderModelTexture(spec)
	if err != nil {
		return nil, err
	}
	stored.model = &builtModel{spec: spec, mesh: buildHumanoid(geometrySeed, spec.Subdivision), texture: texture}
	return stored.model, nil
}

func (a *Adapter) rememberHue(task string, hue float64) {
	a.hueMu.Lock()
	defer a.hueMu.Unlock()
	if a.hues == nil {
		a.hues = map[string]float64{}
	}
	a.hues[task] = hue
}

func (a *Adapter) rememberedHue(task string) (float64, bool) {
	a.hueMu.Lock()
	defer a.hueMu.Unlock()
	hue, ok := a.hues[task]
	return hue, ok
}

// buildSplat emits finite, visible 32-byte raw splats at the mock mesh vertices.
// Its quaternion byte order is w,x,y,z; it is a test representation, not a mesh.
func buildSplat(mesh meshData, seed [32]byte) []byte {
	raw := make([]byte, mesh.vertexCount()*32)
	for point := 0; point < mesh.vertexCount(); point++ {
		record := raw[point*32:][:32]
		for axis := 0; axis < 3; axis++ {
			binary.LittleEndian.PutUint32(record[axis*4:], math.Float32bits(mesh.position[point*3+axis]))
			binary.LittleEndian.PutUint32(record[12+axis*4:], math.Float32bits(0.015))
		}
		copy(record[24:28], []byte{seed[0], seed[1], seed[2], 255})
		copy(record[28:32], []byte{255, 128, 128, 128})
	}
	return raw
}

// declaresMaterial reports whether the Offering delivers an OBJ material
// library (a model slot of variant mtl) for its OBJ to reference.
// objMaterialOf decides the OBJ's material reference: the library the result
// delivers, or for a format conversion that delivers none the dangling
// temp.mtl of the real Meshy Convert export (#330); other results without an
// MTL slot stay geometry only.
func objMaterialOf(offering generation.Offering) objMaterial {
	switch {
	case declaresMaterial(offering):
		return objDeliveredLibrary
	case offering.Capability == generation.FormatConversion:
		return objDanglingLibrary
	}
	return objGeometryOnly
}

func declaresMaterial(offering generation.Offering) bool {
	for _, output := range offering.Outputs {
		if output.Role == "model" && output.Variant == "mtl" {
			return true
		}
	}
	return false
}
