package modelinspect

import (
	"encoding/binary"
	"math"
)

// decodeF32 decodes an accessor into float32 values. Positions, normals, UVs
// and colors share this path, so every component type is converted once and
// stored in the narrowest type the renderer needs.
func (m *Model) decodeF32(index int) ([]float32, error) {
	if index < 0 || index >= len(m.accessors) {
		return nil, invalidf("accessor %d is out of range", index)
	}
	m.cache.mu.Lock()
	defer m.cache.mu.Unlock()
	if values, ok := m.cache.f32[index]; ok {
		return values, nil
	}
	values, err := m.decodeAccessorF32(m.accessors[index])
	if err != nil {
		return nil, err
	}
	m.cache.f32[index] = values
	m.decodedBytes.Add(int64(cap(values)) * 4)
	return values, nil
}

func (m *Model) decodeAccessorF32(a accessor) ([]float32, error) {
	if a.count == 0 {
		return []float32{}, nil
	}
	raw, err := m.readAccessor(a)
	if err != nil {
		return nil, err
	}
	total := a.count * a.components
	if total < 0 || total > maxIntValue {
		return nil, invalidf("accessor element count is too large")
	}
	out := make([]float32, total)
	for i := 0; i < a.count; i++ {
		base := i * a.stride
		for component := 0; component < a.components; component++ {
			offset := base + component*a.componentSize
			if offset < 0 || offset+a.componentSize > len(raw) {
				return nil, invalidf("accessor payload is truncated")
			}
			out[i*a.components+component] = decodeComponentF32(
				a.componentType, a.normalized, raw[offset:offset+a.componentSize],
			)
		}
	}
	return out, nil
}

func decodeComponentF32(componentType int, normalized bool, raw []byte) float32 {
	switch componentType {
	case 5120: // BYTE
		value := int8(raw[0])
		if normalized {
			return float32(math.Max(float64(value)/127, -1))
		}
		return float32(value)
	case 5121: // UNSIGNED_BYTE
		value := raw[0]
		if normalized {
			return float32(float64(value) / 255)
		}
		return float32(value)
	case 5122: // SHORT
		value := int16(binary.LittleEndian.Uint16(raw))
		if normalized {
			return float32(math.Max(float64(value)/32767, -1))
		}
		return float32(value)
	case 5123: // UNSIGNED_SHORT
		value := binary.LittleEndian.Uint16(raw)
		if normalized {
			return float32(float64(value) / 65535)
		}
		return float32(value)
	case 5125: // UNSIGNED_INT
		value := binary.LittleEndian.Uint32(raw)
		if normalized {
			return float32(float64(value) / 4294967295)
		}
		return float32(value)
	case 5126: // FLOAT
		return math.Float32frombits(binary.LittleEndian.Uint32(raw))
	default:
		return 0
	}
}

// decodeU32 decodes an index accessor into uint32. It is the only integer
// representation the rest of the package uses.
func (m *Model) decodeU32(index int) ([]uint32, error) {
	if index < 0 || index >= len(m.accessors) {
		return nil, invalidf("accessor %d is out of range", index)
	}
	m.cache.mu.Lock()
	defer m.cache.mu.Unlock()
	if values, ok := m.cache.u32[index]; ok {
		return values, nil
	}
	values, err := m.decodeAccessorU32(m.accessors[index])
	if err != nil {
		return nil, err
	}
	m.cache.u32[index] = values
	m.decodedBytes.Add(int64(cap(values)) * 4)
	return values, nil
}

func (m *Model) decodeAccessorU32(a accessor) ([]uint32, error) {
	if a.count == 0 {
		return []uint32{}, nil
	}
	if a.components != 1 {
		return nil, invalidf("index accessor is not scalar")
	}
	raw, err := m.readAccessor(a)
	if err != nil {
		return nil, err
	}
	out := make([]uint32, a.count)
	for i := 0; i < a.count; i++ {
		offset := i * a.stride
		var value uint32
		switch a.componentType {
		case 5121:
			value = uint32(raw[offset])
		case 5123:
			if offset+2 > len(raw) {
				return nil, invalidf("index accessor payload is truncated")
			}
			value = uint32(binary.LittleEndian.Uint16(raw[offset:]))
		case 5125:
			if offset+4 > len(raw) {
				return nil, invalidf("index accessor payload is truncated")
			}
			value = binary.LittleEndian.Uint32(raw[offset:])
		default:
			return nil, invalidf("index accessor has an invalid component type")
		}
		out[i] = value
	}
	return out, nil
}

// readAccessor returns the bytes one accessor spans. The caller holds
// m.cache.mu, which also guards the decoded meshopt views.
func (m *Model) readAccessor(a accessor) ([]byte, error) {
	if a.count <= 0 {
		return nil, nil
	}
	span := int64(a.stride)*(int64(a.count)-1) + int64(a.elemSize)
	if span <= 0 || span > int64(maxIntValue) {
		return nil, invalidf("accessor payload is too large")
	}
	if a.view >= 0 && m.bufferViews[a.view].meshopt != nil {
		view, err := m.meshoptViewLocked(a.view)
		if err != nil {
			return nil, err
		}
		if a.viewOffset < 0 || a.viewOffset > int64(len(view)) || span > int64(len(view))-a.viewOffset {
			return nil, invalidf("accessor payload is outside its decoded bufferView")
		}
		return view[a.viewOffset : a.viewOffset+span], nil
	}
	return readAt(m.src, a.srcOffset, span)
}

// meshoptViewLocked returns the decoded bytes of a compressed bufferView,
// decoding it once. A failed decode is not cached, so a transient read error
// can succeed later; the caller holds m.cache.mu.
func (m *Model) meshoptViewLocked(index int) ([]byte, error) {
	if data, ok := m.cache.meshopt[index]; ok {
		return data, nil
	}
	view := m.bufferViews[index]
	compressed, err := readAt(m.src, view.meshopt.offset, view.meshopt.length)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeMeshopt(view.meshopt, compressed)
	if err != nil {
		return nil, err
	}
	m.cache.meshopt[index] = decoded[:view.length]
	m.decodedBytes.Add(int64(cap(decoded)))
	return decoded[:view.length], nil
}

func (m *Model) positions(p *part) ([]float32, error) {
	values, err := m.decodeF32(p.position)
	if err != nil {
		return nil, err
	}
	if len(values) != p.vertexCount*3 {
		return nil, invalidf("position accessor has an unexpected layout")
	}
	return values, nil
}

func (m *Model) normals(p *part) ([]float32, error) {
	if p.normal < 0 {
		return nil, nil
	}
	values, err := m.decodeF32(p.normal)
	if err != nil {
		return nil, err
	}
	if len(values) != p.vertexCount*3 {
		return nil, invalidf("normal accessor has an unexpected layout")
	}
	return values, nil
}

func (m *Model) texCoords(p *part, set int) ([]float32, error) {
	accessor, ok := p.uv[set]
	if !ok {
		return nil, nil
	}
	values, err := m.decodeF32(accessor)
	if err != nil {
		return nil, err
	}
	if len(values) != p.vertexCount*2 {
		return nil, invalidf("texcoord accessor has an unexpected layout")
	}
	return values, nil
}

func (m *Model) colors(p *part) ([]float32, error) {
	if p.color < 0 {
		return nil, nil
	}
	values, err := m.decodeF32(p.color)
	if err != nil {
		return nil, err
	}
	if len(values) != p.vertexCount*p.colorComponents {
		return nil, invalidf("color accessor has an unexpected layout")
	}
	return values, nil
}

// validatedIndices returns decoded indices and whether all of them address a
// position. A non-indexed primitive returns nil, true.
func (m *Model) validatedIndices(p *part) ([]uint32, bool) {
	if p.index < 0 {
		return nil, true
	}
	indices, err := m.decodeU32(p.index)
	if err != nil {
		return nil, false
	}
	for _, value := range indices {
		if int(value) >= p.vertexCount {
			return nil, false
		}
	}
	return indices, true
}

// corner returns the position element used by vertex j of a primitive.
func (p *part) corner(j int, indices []uint32) (uint32, bool) {
	if p.index < 0 {
		if j < 0 || j >= p.vertexCount {
			return 0, false
		}
		return uint32(j), true
	}
	if j < 0 || j >= len(indices) {
		return 0, false
	}
	return indices[j], true
}

func (p *part) elementCount(indices []uint32) int {
	if p.index >= 0 {
		return len(indices)
	}
	return p.vertexCount
}

// triangle returns triangle i in a consistent winding for every supported
// primitive mode. Modes 0-3 have no triangles and are never queried.
func (p *part) triangle(i int, indices []uint32) (a, b, c uint32, ok bool) {
	var ia, ib, ic int
	switch p.mode {
	case 4:
		ia, ib, ic = 3*i, 3*i+1, 3*i+2
	case 5:
		ia, ib, ic = i, i+1, i+2
		if i%2 == 1 {
			ia, ib = ib, ia
		}
	case 6:
		ia, ib, ic = 0, i+1, i+2
	default:
		return 0, 0, 0, false
	}
	a, okA := p.corner(ia, indices)
	b, okB := p.corner(ib, indices)
	c, okC := p.corner(ic, indices)
	if !okA || !okB || !okC {
		return 0, 0, 0, false
	}
	return a, b, c, true
}

// partTriangles returns the number of triangles implied by the primitive mode
// and the actual element count.
func (p *part) partTriangles(indices []uint32) int {
	n := p.elementCount(indices)
	switch p.mode {
	case 4:
		return n / 3
	case 5, 6:
		if n < 3 {
			return 0
		}
		return n - 2
	default:
		return 0
	}
}
