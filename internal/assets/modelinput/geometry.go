package modelinput

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"
)

type view struct{ offset, length, stride, target int64 }
type accessor struct {
	view                                           int
	offset, count, width, components, kind, stride int64
	normalized                                     bool
	min, max, actualMin, actualMax                 []float64
	maximum                                        uint64
}

func (v *validator) buffers(doc map[string]any) {
	buffers := v.array(doc, "buffers", true)
	if len(buffers) != 1 {
		v.fail(Unsupported, "buffer_count")
		return
	}
	buffer := v.object(buffers[0])
	v.fields(buffer, "byteLength", "uri")
	if _, ok := buffer["uri"]; ok {
		v.fail(Unsupported, "uri")
		return
	}
	size := v.integer(buffer, "byteLength", 0, true)
	if size <= 0 || size > int64(len(v.bin)) || int64(len(v.bin))-size > 3 {
		v.fail(Invalid, "buffer_length")
		return
	}
	for _, padding := range v.bin[size:] {
		if padding != 0 {
			v.fail(Invalid, "bin_padding")
			return
		}
	}
	for _, raw := range v.array(doc, "bufferViews", true) {
		o := v.object(raw)
		v.fields(o, "buffer", "byteOffset", "byteLength", "byteStride", "target")
		b := v.integer(o, "buffer", 0, true)
		entry := view{offset: v.integer(o, "byteOffset", 0, false), length: v.integer(o, "byteLength", 0, true), stride: v.integer(o, "byteStride", 0, false), target: v.integer(o, "target", 0, false)}
		if b != 0 || entry.length <= 0 || entry.offset > size || entry.length > size-entry.offset {
			v.fail(Invalid, "buffer_view_range")
			return
		}
		if _, ok := o["byteStride"]; ok && (entry.stride < 4 || entry.stride > 252 || entry.stride%4 != 0) {
			v.fail(Invalid, "buffer_view_stride")
			return
		}
		if entry.target != 0 && entry.target != 34962 && entry.target != 34963 {
			v.fail(Invalid, "buffer_view_target")
			return
		}
		v.views = append(v.views, entry)
	}
}
func (v *validator) readAccessors(doc map[string]any) {
	for _, raw := range v.array(doc, "accessors", true) {
		o := v.object(raw)
		if _, ok := o["sparse"]; ok {
			v.fail(Unsupported, "sparse")
			return
		}
		v.fields(o, "bufferView", "byteOffset", "componentType", "normalized", "count", "type", "min", "max")
		a := accessor{view: v.index(o, "bufferView", len(v.views), true), offset: v.integer(o, "byteOffset", 0, false), count: v.integer(o, "count", 0, true), kind: v.integer(o, "componentType", 0, true), normalized: v.boolean(o, "normalized", false)}
		switch a.kind {
		case 5120, 5121:
			a.width = 1
		case 5122, 5123:
			a.width = 2
		case 5125, 5126:
			a.width = 4
		default:
			v.fail(Invalid, "accessor_component_type")
		}
		switch v.text(o, "type", "", true) {
		case "SCALAR":
			a.components = 1
		case "VEC2":
			a.components = 2
		case "VEC3":
			a.components = 3
		case "VEC4":
			a.components = 4
		case "MAT2", "MAT3", "MAT4":
			v.fail(Unsupported, "matrix_accessor")
		default:
			v.fail(Invalid, "accessor_type")
		}
		if v.err != nil {
			return
		}
		if a.count <= 0 || a.normalized && (a.kind == 5126 || a.kind == 5125) {
			v.fail(Invalid, "accessor_normalization")
			return
		}
		entry := v.views[a.view]
		a.stride = entry.stride
		if a.stride == 0 {
			a.stride = a.width * a.components
		}
		if a.offset%a.width != 0 || (entry.offset+a.offset)%a.width != 0 || a.stride%a.width != 0 || a.stride < a.width*a.components || a.offset > entry.length || a.width*a.components > entry.length-a.offset || (a.count-1) > (entry.length-a.offset-a.width*a.components)/a.stride {
			v.fail(Invalid, "accessor_range")
			return
		}
		a.min = v.floats(o, "min", int(a.components), false)
		a.max = v.floats(o, "max", int(a.components), false)
		a.actualMin = make([]float64, a.components)
		a.actualMax = make([]float64, a.components)
		for j := range a.actualMin {
			a.actualMin[j] = math.Inf(1)
			a.actualMax[j] = math.Inf(-1)
		}
		// Each accessor is decoded once; primitives can share the resulting summary.
		v.work(a.count * a.components)
		if v.err != nil {
			return
		}
		for i := int64(0); i < a.count; i++ {
			for j := int64(0); j < a.components; j++ {
				rawAccessor := a
				rawAccessor.normalized = false
				n := v.component(rawAccessor, i, j)
				if math.IsNaN(n) || math.IsInf(n, 0) {
					v.fail(Invalid, "nonfinite_geometry")
					return
				}
				a.actualMin[j] = min(a.actualMin[j], n)
				a.actualMax[j] = max(a.actualMax[j], n)
				if (a.kind == 5121 || a.kind == 5123 || a.kind == 5125) && n >= 0 && n > float64(a.maximum) {
					a.maximum = uint64(n)
				}
			}
		}
		for j := int64(0); j < a.components; j++ {
			if a.min != nil && a.max != nil && a.min[j] > a.max[j] {
				v.fail(Invalid, "accessor_bounds")
				return
			}
			cast := func(n float64) float64 {
				if a.kind == 5126 {
					return float64(float32(n))
				}
				return n
			}
			if a.min != nil && (math.IsInf(cast(a.min[j]), 0) || cast(a.min[j]) != a.actualMin[j]) {
				v.fail(Invalid, "accessor_bounds")
				return
			}
			if a.max != nil && (math.IsInf(cast(a.max[j]), 0) || cast(a.max[j]) != a.actualMax[j]) {
				v.fail(Invalid, "accessor_bounds")
				return
			}
		}
		if v.err != nil {
			return
		}
		v.accessors = append(v.accessors, a)
	}
}
func (v *validator) component(a accessor, i, j int64) float64 {
	offset := v.views[a.view].offset + a.offset + i*a.stride + j*a.width
	b := v.bin[offset : offset+a.width]
	switch a.kind {
	case 5120:
		n := float64(int8(b[0]))
		if a.normalized {
			return max(n/127, -1)
		}
		return n
	case 5121:
		n := float64(b[0])
		if a.normalized {
			return n / 255
		}
		return n
	case 5122:
		n := float64(int16(binary.LittleEndian.Uint16(b)))
		if a.normalized {
			return max(n/32767, -1)
		}
		return n
	case 5123:
		n := float64(binary.LittleEndian.Uint16(b))
		if a.normalized {
			return n / 65535
		}
		return n
	case 5125:
		return float64(binary.LittleEndian.Uint32(b))
	case 5126:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	}
	return 0
}
func attributeSet(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(name, prefix)
	if suffix == "" {
		return false
	}
	n, err := strconv.ParseUint(suffix, 10, 31)
	return err == nil && strconv.FormatUint(n, 10) == suffix
}

// A set with n unique nonnegative indices is consecutive from zero exactly
// when its maximum is n-1. Work stays bounded by the actual attribute count,
// even if a supplied index is enormous.
func (v *validator) attributeSets(attrs map[string]any) {
	for _, prefix := range []string{"TEXCOORD_", "COLOR_"} {
		count, maximum := int64(0), int64(-1)
		for name := range attrs {
			if !attributeSet(name, prefix) {
				continue
			}
			index, _ := strconv.ParseInt(strings.TrimPrefix(name, prefix), 10, 32)
			count++
			maximum = max(maximum, index)
			v.work(1)
		}
		if maximum != count-1 {
			v.fail(Invalid, "attribute_set_gap")
		}
	}
}

func (v *validator) meshes(doc map[string]any) int {
	meshes := v.array(doc, "meshes", true)
	if len(meshes) == 0 {
		v.fail(Invalid, "empty_geometry")
		return 0
	}
	uses := map[int]map[int]bool{}
	viewKinds := map[int]string{}
	useView := func(a accessor, kind string) {
		if other := viewKinds[a.view]; other != "" && other != kind {
			v.fail(Invalid, "mixed_buffer_view")
		}
		viewKinds[a.view] = kind
	}
	v.texturedMeshes = make([]bool, len(meshes))
	v.meshTriangles = make([]int64, len(meshes))
	v.profile.HasSingleBaseColorTexture = true
	singleBaseColorTexture := -1
	for meshIndex, raw := range meshes {
		m := v.object(raw)
		if _, ok := m["weights"]; ok {
			v.fail(Unsupported, "morph")
			return 0
		}
		v.fields(m, "primitives")
		primitives := v.array(m, "primitives", true)
		if len(primitives) == 0 {
			v.fail(Invalid, "empty_primitive")
			return 0
		}
		for _, item := range primitives {
			p := v.object(item)
			if _, ok := p["targets"]; ok {
				v.fail(Unsupported, "morph")
				return 0
			}
			v.fields(p, "attributes", "indices", "material", "mode")
			if v.integer(p, "mode", 4, false) != 4 {
				v.fail(Unsupported, "primitive_mode")
				return 0
			}
			attrs := v.object(p["attributes"])
			v.attributeSets(attrs)
			position := v.index(attrs, "POSITION", len(v.accessors), true)
			if v.err != nil {
				return 0
			}
			pos := v.accessors[position]
			if pos.kind != 5126 || pos.components != 3 || pos.normalized || pos.min == nil || pos.max == nil {
				v.fail(Invalid, "position_accessor")
				return 0
			}
			for name := range attrs {
				if strings.HasPrefix(name, "JOINTS_") || strings.HasPrefix(name, "WEIGHTS_") {
					v.fail(Unsupported, "skin")
					return 0
				}
				id := v.index(attrs, name, len(v.accessors), true)
				if v.err != nil {
					return 0
				}
				a := v.accessors[id]
				if a.count != pos.count || a.offset%4 != 0 || a.stride%4 != 0 || v.views[a.view].target == 34963 {
					v.fail(Invalid, "attribute_layout")
					return 0
				}
				useView(a, "attribute")
				if uses[a.view] == nil {
					uses[a.view] = map[int]bool{}
				}
				uses[a.view][id] = true
				key := strconv.Itoa(id) + ":" + name
				if v.checkedAttributes == nil {
					v.checkedAttributes = map[string]bool{}
				}
				if !v.checkedAttributes[key] {
					v.attribute(name, a)
					v.checkedAttributes[key] = v.err == nil
				}
				if v.err != nil {
					return 0
				}
			}
			count := pos.count
			if _, ok := p["indices"]; ok {
				id := v.index(p, "indices", len(v.accessors), true)
				if v.err != nil {
					return 0
				}
				a := v.accessors[id]
				if (a.kind != 5121 && a.kind != 5123 && a.kind != 5125) || a.components != 1 || a.normalized || v.views[a.view].stride != 0 || v.views[a.view].target == 34962 {
					v.fail(Invalid, "index_accessor")
					return 0
				}
				useView(a, "index")
				restart := uint64(1)<<(a.width*8) - 1
				if a.maximum >= uint64(pos.count) || a.maximum == restart {
					v.fail(Invalid, "index_range")
					return 0
				}
				count = a.count
			}
			if count < 3 || count%3 != 0 {
				v.fail(Invalid, "triangle_count")
				return 0
			}
			v.profile.Vertices += pos.count
			v.profile.Triangles += count / 3
			v.meshTriangles[meshIndex] += count / 3
			if v.profile.Vertices > v.policy.Limits.MaxVertices {
				v.fail(Limit, "vertex_limit")
				return 0
			}
			if v.profile.Triangles > v.policy.Limits.MaxTriangles {
				v.fail(Limit, "triangle_limit")
				return 0
			}
			material := v.index(p, "material", len(v.materialUV), false)
			if v.err != nil {
				return 0
			}
			if material < 0 || v.materialBaseColorTextures[material] < 0 {
				v.profile.HasSingleBaseColorTexture = false
			} else if singleBaseColorTexture < 0 {
				singleBaseColorTexture = v.materialBaseColorTextures[material]
			} else if singleBaseColorTexture != v.materialBaseColorTextures[material] {
				v.profile.HasSingleBaseColorTexture = false
			}
			if material >= 0 {
				for _, set := range v.materialUV[material] {
					if _, ok := attrs["TEXCOORD_"+strconv.FormatInt(set, 10)]; !ok {
						v.fail(Invalid, "material_texcoord")
						return 0
					}
					v.texturedMeshes[meshIndex] = true
				}
			}
			v.work(1)
			if v.err != nil {
				return 0
			}
		}
	}
	for view, ids := range uses {
		if len(ids) > 1 && v.views[view].stride == 0 {
			v.fail(Invalid, "shared_attribute_stride")
		}
	}
	return len(meshes)
}
func (v *validator) attribute(name string, a accessor) {
	switch {
	case name == "POSITION":
		if a.kind != 5126 || a.components != 3 || a.normalized {
			v.fail(Invalid, "position_accessor")
		}
	case name == "NORMAL" || name == "TANGENT":
		components := int64(3)
		if name == "TANGENT" {
			components = 4
		}
		if a.kind != 5126 || a.components != components || a.normalized {
			v.fail(Invalid, "normal_accessor")
			return
		}
		v.work(a.count * a.components)
		if v.err != nil {
			return
		}
		for i := int64(0); i < a.count; i++ {
			var norm float64
			for j := int64(0); j < 3; j++ {
				n := v.component(a, i, j)
				norm += n * n
			}
			if math.Abs(norm-1) > 0.0001 {
				v.fail(Invalid, "normal_length")
				return
			}
			if components == 4 {
				w := v.component(a, i, 3)
				if w != 1 && w != -1 {
					v.fail(Invalid, "tangent_sign")
					return
				}
			}
		}
	case attributeSet(name, "TEXCOORD_"):
		if a.components != 2 || !((a.kind == 5126 && !a.normalized) || ((a.kind == 5121 || a.kind == 5123) && a.normalized)) {
			v.fail(Invalid, "texcoord_accessor")
		}
	case attributeSet(name, "COLOR_"):
		if (a.components != 3 && a.components != 4) || !((a.kind == 5126 && !a.normalized) || ((a.kind == 5121 || a.kind == 5123) && a.normalized)) {
			v.fail(Invalid, "color_accessor")
			return
		}
		for i := range a.actualMin {
			if !a.normalized && (a.actualMin[i] < 0 || a.actualMax[i] > 1) {
				v.fail(Invalid, "color_range")
			}
		}
	default:
		v.fail(Unsupported, "attribute_semantic")
	}
}
