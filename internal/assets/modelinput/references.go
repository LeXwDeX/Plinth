package modelinput

import (
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"strconv"
)

func (v *validator) document(doc map[string]any) {
	for _, key := range []string{"skins", "animations"} {
		if _, ok := doc[key]; ok {
			reason := "skin"
			if key == "animations" {
				reason = "animation"
			}
			v.fail(Unsupported, reason)
			return
		}
	}
	if _, ok := doc["cameras"]; ok {
		v.fail(Unsupported, "camera")
		return
	}
	v.fields(doc, "asset", "buffers", "bufferViews", "accessors", "meshes", "nodes", "scenes", "scene", "materials", "textures", "images", "samplers")
	asset := v.object(doc["asset"])
	v.fields(asset, "version", "minVersion", "generator", "copyright")
	if v.text(asset, "version", "", true) != "2.0" || v.text(asset, "minVersion", "2.0", false) != "2.0" {
		v.fail(Unsupported, "gltf_version")
		return
	}
	for _, key := range []string{"generator", "copyright"} {
		if _, ok := asset[key]; ok {
			v.text(asset, key, "", true)
		}
	}
	v.buffers(doc)
	if v.err != nil {
		return
	}
	v.readAccessors(doc)
	if v.err != nil {
		return
	}
	v.materials(doc)
	if v.err != nil {
		return
	}
	meshes := v.meshes(doc)
	if v.err != nil {
		return
	}
	v.nodes(doc, meshes)
}
func (v *validator) materials(doc map[string]any) {
	images := v.array(doc, "images", false)
	for _, raw := range images {
		o := v.object(raw)
		if _, ok := o["uri"]; ok {
			v.fail(Unsupported, "uri")
			return
		}
		v.fields(o, "bufferView", "mimeType")
		id := v.index(o, "bufferView", len(v.views), true)
		mime := v.text(o, "mimeType", "", true)
		if v.err != nil {
			return
		}
		if mime != "image/png" && mime != "image/jpeg" {
			v.fail(Unsupported, "image_format")
			return
		}
		entry := v.views[id]
		if entry.stride != 0 || entry.target != 0 {
			v.fail(Invalid, "image_buffer_view")
			return
		}
		key := strconv.FormatInt(entry.offset, 10) + ":" + strconv.FormatInt(entry.length, 10) + ":" + mime
		if !v.imageKeys[key] {
			data := v.bin[entry.offset : entry.offset+entry.length]
			v.work(entry.length)
			if v.err != nil {
				return
			}
			config, format, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil || mime == "image/png" && format != "png" || mime == "image/jpeg" && format != "jpeg" {
				v.fail(Invalid, "image_structure")
				return
			}
			if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > v.policy.Limits.MaxImagePixels/int64(config.Height) {
				v.fail(Limit, "image_pixel_limit")
				return
			}
			pixels := int64(config.Width) * int64(config.Height)
			if pixels > v.policy.Limits.MaxTotalImagePixels-v.pixels {
				v.fail(Limit, "total_image_pixel_limit")
				return
			}
			v.pixels += pixels
			if v.policy.generated() {
				// Header-bounded only: the bytes are copied, never decoded.
				v.imageKeys[key] = true
				continue
			}
			// Reserve the actual unique image structure and pixel work before the
			// existing shared raster checker can allocate/decode those pixels.
			v.work(2 * pixels)
			if v.err != nil {
				return
			}
			decoded, decodedFormat, decodeErr := image.Decode(contextReader{v.ctx, bytes.NewReader(data)})
			if v.ctx.Err() != nil {
				v.fail(Canceled, "context_canceled")
				return
			}
			if decodeErr != nil || decodedFormat != format || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
				v.fail(Invalid, "image_pixels")
				return
			}
			v.imageKeys[key] = true
		}
	}
	samplers := v.array(doc, "samplers", false)
	for _, raw := range samplers {
		o := v.object(raw)
		v.fields(o, "magFilter", "minFilter", "wrapS", "wrapT")
		for _, key := range []string{"magFilter", "minFilter", "wrapS", "wrapT"} {
			if _, ok := o[key]; !ok {
				continue
			}
			n := v.integer(o, key, 0, true)
			valid := false
			switch key {
			case "magFilter":
				valid = n == 9728 || n == 9729
			case "minFilter":
				valid = n == 9728 || n == 9729 || n >= 9984 && n <= 9987
			default:
				valid = n == 33071 || n == 33648 || n == 10497
			}
			if !valid {
				v.fail(Invalid, "sampler_enum")
			}
		}
	}
	textures := v.array(doc, "textures", false)
	for _, raw := range textures {
		o := v.object(raw)
		v.fields(o, "source", "sampler")
		v.index(o, "source", len(images), true)
		v.index(o, "sampler", len(samplers), false)
	}
	for _, raw := range v.array(doc, "materials", false) {
		o := v.object(raw)
		v.fields(o, "pbrMetallicRoughness", "normalTexture", "occlusionTexture", "emissiveTexture", "emissiveFactor", "alphaMode", "alphaCutoff", "doubleSided")
		uv := []int64{}
		baseColorTexture := -1
		textureInfo := func(raw any, kind string) {
			t := v.object(raw)
			keys := []string{"index", "texCoord"}
			if kind == "normal" {
				keys = append(keys, "scale")
			}
			if kind == "occlusion" {
				keys = append(keys, "strength")
			}
			v.fields(t, keys...)
			texture := v.index(t, "index", len(textures), true)
			if kind == "base" {
				baseColorTexture = texture
			}
			uv = append(uv, v.integer(t, "texCoord", 0, false))
			if n, ok := t["scale"]; ok {
				v.numeric(n)
			}
			if n, ok := t["strength"]; ok {
				v.unit(v.numeric(n))
			}
		}
		if raw, ok := o["pbrMetallicRoughness"]; ok {
			p := v.object(raw)
			v.fields(p, "baseColorFactor", "baseColorTexture", "metallicFactor", "roughnessFactor", "metallicRoughnessTexture")
			if _, ok := p["baseColorFactor"]; ok {
				for _, n := range v.floats(p, "baseColorFactor", 4, true) {
					v.unit(n)
				}
			}
			for _, key := range []string{"metallicFactor", "roughnessFactor"} {
				if n, ok := p[key]; ok {
					v.unit(v.numeric(n))
				}
			}
			for _, key := range []string{"baseColorTexture", "metallicRoughnessTexture"} {
				if t, ok := p[key]; ok {
					kind := ""
					if key == "baseColorTexture" {
						kind = "base"
					}
					textureInfo(t, kind)
				}
			}
		}
		for _, key := range []string{"normalTexture", "occlusionTexture", "emissiveTexture"} {
			if t, ok := o[key]; ok {
				kind := ""
				if key == "normalTexture" {
					kind = "normal"
				}
				if key == "occlusionTexture" {
					kind = "occlusion"
				}
				textureInfo(t, kind)
			}
		}
		if _, ok := o["emissiveFactor"]; ok {
			for _, n := range v.floats(o, "emissiveFactor", 3, true) {
				v.unit(n)
			}
		}
		mode := v.text(o, "alphaMode", "OPAQUE", false)
		if mode != "OPAQUE" && mode != "MASK" && mode != "BLEND" {
			v.fail(Invalid, "alpha_mode")
		}
		if raw, ok := o["alphaCutoff"]; ok {
			if v.numeric(raw) < 0 {
				v.fail(Invalid, "alpha_cutoff")
			}
		}
		v.boolean(o, "doubleSided", false)
		v.materialUV = append(v.materialUV, uv)
		v.materialBaseColorTextures = append(v.materialBaseColorTextures, baseColorTexture)
	}
}
func (v *validator) unit(n float64) {
	if n < 0 || n > 1 {
		v.fail(Invalid, "material_factor")
	}
}
func (v *validator) nodes(doc map[string]any, meshes int) {
	nodes := v.array(doc, "nodes", true)
	v.profile.Nodes = int64(len(nodes))
	if v.profile.Nodes > v.policy.Limits.MaxNodes {
		v.fail(Limit, "node_limit")
		return
	}
	if len(nodes) == 0 {
		v.fail(Invalid, "empty_scene")
		return
	}
	children := make([][]int, len(nodes))
	parents := make([]int, len(nodes))
	nodeMesh := make([]int, len(nodes))
	referencedMeshes := make([]bool, meshes)
	// Count unused definitions once and add only the additional node instances.
	// Parser ceilings keep this product bounded without expanding mesh geometry.
	v.profile.InstancedTriangles = v.profile.Triangles
	for i, raw := range nodes {
		o := v.object(raw)
		if _, ok := o["skin"]; ok {
			v.fail(Unsupported, "skin")
			return
		}
		if _, ok := o["weights"]; ok {
			v.fail(Unsupported, "morph")
			return
		}
		if _, ok := o["camera"]; ok {
			v.fail(Unsupported, "camera")
			return
		}
		v.fields(o, "mesh", "children", "matrix", "translation", "rotation", "scale")
		mesh := v.index(o, "mesh", meshes, false)
		nodeMesh[i] = mesh
		if _, matrix := o["matrix"]; matrix {
			matrix := v.floats(o, "matrix", 16, true)
			if len(matrix) == 16 {
				v.affineMatrix(matrix)
			}
			for _, key := range []string{"translation", "rotation", "scale"} {
				if _, ok := o[key]; ok {
					v.fail(Invalid, "mixed_node_transform")
				}
			}
		} else {
			for _, key := range []string{"translation", "scale"} {
				v.floats(o, key, 3, false)
			}
			if rotation := v.floats(o, "rotation", 4, false); len(rotation) == 4 {
				var norm float64
				for _, n := range rotation {
					norm += n * n
				}
				if math.Abs(norm-1) > 0.0001 {
					v.fail(Invalid, "node_rotation")
				}
			}
		}
		for _, raw := range v.array(o, "children", false) {
			index := v.integer(map[string]any{"child": raw}, "child", 0, true)
			if index >= int64(len(nodes)) {
				v.fail(Invalid, "invalid_reference")
				return
			}
			parents[index]++
			if parents[index] > 1 {
				v.fail(Invalid, "multiple_node_parents")
				return
			}
			children[i] = append(children[i], int(index))
		}
		if v.err != nil {
			return
		}
		if mesh >= 0 {
			if referencedMeshes[mesh] {
				v.profile.InstancedTriangles += v.meshTriangles[mesh]
			}
			referencedMeshes[mesh] = true
		}
	}
	// Iterative DFS has bounded memory even for a deeply nested node hierarchy.
	colors := make([]uint8, len(nodes))
	type visit struct {
		node int
		exit bool
	}
	for start := range nodes {
		if colors[start] != 0 {
			continue
		}
		stack := []visit{{node: start}}
		for len(stack) > 0 {
			item := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			v.work(1)
			if v.err != nil {
				return
			}
			if item.exit {
				colors[item.node] = 2
				continue
			}
			if colors[item.node] == 1 {
				v.fail(Invalid, "node_cycle")
				return
			}
			if colors[item.node] == 2 {
				continue
			}
			colors[item.node] = 1
			stack = append(stack, visit{item.node, true})
			for _, child := range children[item.node] {
				stack = append(stack, visit{child, false})
			}
		}
	}
	scenes := v.array(doc, "scenes", true)
	if len(scenes) == 0 {
		v.fail(Invalid, "empty_scene")
		return
	}
	selected := v.index(doc, "scene", len(scenes), false)
	if selected < 0 {
		selected = 0
	}
	if v.err != nil {
		return
	}
	renderable := false
	for sceneIndex, raw := range scenes {
		o := v.object(raw)
		v.fields(o, "nodes")
		roots := v.array(o, "nodes", false)
		seen := map[int]bool{}
		for _, raw := range roots {
			index := v.integer(map[string]any{"node": raw}, "node", 0, true)
			if index >= int64(len(nodes)) {
				v.fail(Invalid, "invalid_reference")
				return
			}
			if seen[int(index)] || parents[index] != 0 {
				v.fail(Invalid, "scene_root")
				return
			}
			seen[int(index)] = true
			if selected >= 0 && selected != sceneIndex {
				continue
			}
			stack := []int{int(index)}
			for len(stack) > 0 {
				n := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				v.work(1)
				if v.err != nil {
					return
				}
				if nodeMesh[n] >= 0 {
					renderable = true
					v.profile.HasTextures = v.profile.HasTextures || v.texturedMeshes[nodeMesh[n]]
				}
				stack = append(stack, children[n]...)
			}
		}
	}
	if !renderable {
		v.fail(Invalid, "empty_scene")
	}
}

func (v *validator) affineMatrix(matrix []float64) {
	if matrix[3] != 0 || matrix[7] != 0 || matrix[11] != 0 || matrix[15] != 1 {
		v.fail(Invalid, "node_matrix")
		return
	}
	// Core node matrices are affine TRS transforms, not perspective or shear.
	for a := 0; a < 3; a++ {
		for b := a + 1; b < 3; b++ {
			var dot, left, right float64
			for row := 0; row < 3; row++ {
				x, y := matrix[a*4+row], matrix[b*4+row]
				dot += x * y
				left += x * x
				right += y * y
			}
			if left > 0 && right > 0 && math.Abs(dot) > 0.0001*math.Sqrt(left*right) {
				v.fail(Invalid, "node_matrix")
				return
			}
		}
	}
}
