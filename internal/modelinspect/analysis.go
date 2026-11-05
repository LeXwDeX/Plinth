package modelinspect

// partAnalysis is what Inspect learns about one part before measuring it:
// its pose, decoded positions and indices, which vertices its elements use
// and how many drawable triangles it has.
type partAnalysis struct {
	pose      partPose
	positions []float32
	// indices is nil for a non-indexed primitive. indicesOK is false when an
	// index does not address a position.
	indices   []uint32
	indicesOK bool
	// used is a bitset of the vertices the primitive's elements reference.
	// It is nil for a non-indexed primitive, whose first prefix vertices are
	// the ones used.
	used   []uint64
	prefix int
	// triangles counts drawable triangles. A strip or fan triangle that
	// repeats an index is how strips are stitched together and restarted,
	// not a face, so it is excluded; a TRIANGLES face that repeats an index
	// is a real degenerate face and still counts.
	triangles int64
}

// uses reports whether the primitive's elements reference vertex.
func (a *partAnalysis) uses(vertex int) bool {
	if a.used == nil {
		return vertex < a.prefix
	}
	return a.used[vertex>>6]&(1<<(uint(vertex)&63)) != 0
}

// usedElements is how many leading elements (indices, or vertices of a
// non-indexed primitive) the primitive mode consumes.
func usedElements(mode, count int) int {
	switch mode {
	case 1: // LINES
		return count - count%2
	case 2, 3: // LINE_LOOP, LINE_STRIP
		if count < 2 {
			return 0
		}
		return count
	case 4: // TRIANGLES
		return count - count%3
	case 5, 6: // TRIANGLE_STRIP, TRIANGLE_FAN
		if count < 3 {
			return 0
		}
		return count
	default: // POINTS
		return count
	}
}

// analyzePart decodes p's positions and indices. A read or decode failure is
// returned as an error; out-of-range indices are not an error but leave
// indicesOK false, mark only the in-range vertices as used and keep the
// declared triangle count.
func (m *Model) analyzePart(p *part) (*partAnalysis, error) {
	positions, err := m.positions(p)
	if err != nil {
		return nil, err
	}
	analysis := &partAnalysis{
		pose:      m.pose(p),
		positions: positions,
		indicesOK: true,
		triangles: p.triangleCount,
	}
	if p.index < 0 {
		analysis.prefix = min(usedElements(p.mode, p.vertexCount), p.vertexCount)
		return analysis, nil
	}
	indices, err := m.decodeU32(p.index)
	if err != nil {
		return nil, err
	}
	analysis.indices = indices
	analysis.used = make([]uint64, (p.vertexCount+63)/64)
	for _, value := range indices[:usedElements(p.mode, len(indices))] {
		if int64(value) >= int64(p.vertexCount) {
			analysis.indicesOK = false
			continue
		}
		analysis.used[value>>6] |= 1 << (value & 63)
	}
	if !analysis.indicesOK {
		return analysis, nil
	}
	if p.mode == 5 || p.mode == 6 {
		count := p.partTriangles(indices)
		drawable := int64(0)
		for triangle := 0; triangle < count; triangle++ {
			a, b, c, ok := p.triangle(triangle, indices)
			if ok && a != b && b != c && a != c {
				drawable++
			}
		}
		analysis.triangles = drawable
	}
	return analysis, nil
}
