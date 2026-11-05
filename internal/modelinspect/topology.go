package modelinspect

import (
	"math"
	"slices"
)

// topologyIssues records data problems that Inspect reports as warnings.
type topologyIssues struct {
	invalidIndices   bool
	invalidPositions bool
}

// computeTopology welds positions per the option tolerance, then counts
// connected components and edge use. Edges are collected as 64-bit keys and
// sorted, which keeps memory linear and is faster than a map for the ~2M
// triangle models this package targets (see BenchmarkTopologyEdgeCounting).
func (m *Model) computeTopology(readOK []bool, worldBox bounds, opts InspectOptions) (TopologyReport, topologyIssues) {
	// Watertight stays false until every triangle has been analysed and the
	// edge counts prove a closed surface.
	var report TopologyReport
	var issues topologyIssues
	if !worldBox.valid || len(m.parts) == 0 {
		return report, issues
	}
	epsilon := worldBox.diagonal() * opts.WeldEpsilonRatio
	if epsilon < 0 || !finite(epsilon) {
		epsilon = 0
	}
	report.WeldEpsilon = epsilon
	areaEpsilon := epsilon * epsilon

	worldPositions := make([][]float32, len(m.parts))
	vertexIDs := make([][]uint32, len(m.parts))
	weld := make(map[[3]int64]uint32, 1024)
	nextID := uint32(0)
	overflow := false

	for index, p := range m.parts {
		if !readOK[index] || p.triangleCount == 0 {
			continue
		}
		positions, err := m.positions(p)
		if err != nil {
			continue
		}
		if _, ok := m.validatedIndices(p); !ok {
			issues.invalidIndices = true
			continue
		}
		count := p.vertexCount
		if count < 0 || count > maxIntValue/3 {
			continue
		}
		world := make([]float32, count*3)
		ids := make([]uint32, count)
		for vertex := 0; vertex < count; vertex++ {
			point := p.world.transformPoint(
				float64(positions[vertex*3]),
				float64(positions[vertex*3+1]),
				float64(positions[vertex*3+2]),
			)
			if !point.finite() {
				issues.invalidPositions = true
			}
			world[vertex*3] = float32(point[0])
			world[vertex*3+1] = float32(point[1])
			world[vertex*3+2] = float32(point[2])
			key := weldKey(point, epsilon)
			id, found := weld[key]
			if !found {
				if nextID == ^uint32(0) {
					overflow = true
					break
				}
				id = nextID
				nextID++
				weld[key] = id
			}
			ids[vertex] = id
		}
		if overflow {
			break
		}
		worldPositions[index] = world
		vertexIDs[index] = ids
	}
	if overflow {
		// A model with more than 2^32 welded vertices is far outside every
		// supported target; report the facts gathered so far rather than
		// allocating a wider index representation.
		issues.invalidIndices = true
		return report, issues
	}

	weldCount := int(nextID)
	if weldCount == 0 {
		return report, issues
	}
	union := newUnionFind(weldCount)
	totalTriangles := int64(0)
	for _, p := range m.parts {
		totalTriangles += p.triangleCount
	}
	capacity := totalTriangles * 3
	if capacity < 0 || capacity > int64(maxIntValue/8) {
		capacity = 0
	}
	edges := make([]uint64, 0, int(capacity))
	representatives := make([]uint32, 0, int(minInt64(capacity/3, int64(maxIntValue))))

	for index, p := range m.parts {
		ids := vertexIDs[index]
		world := worldPositions[index]
		if ids == nil || !readOK[index] {
			continue
		}
		indices, ok := m.validatedIndices(p)
		if !ok {
			issues.invalidIndices = true
			continue
		}
		count := p.partTriangles(indices)
		for triangle := 0; triangle < count; triangle++ {
			a, b, c, ok := p.triangle(triangle, indices)
			if !ok {
				continue
			}
			wa, wb, wc := ids[a], ids[b], ids[c]
			if wa == wb || wb == wc || wa == wc {
				report.DegenerateTriangles++
				continue
			}
			ax, ay, az := world[a*3], world[a*3+1], world[a*3+2]
			bx, by, bz := world[b*3], world[b*3+1], world[b*3+2]
			cx, cy, cz := world[c*3], world[c*3+1], world[c*3+2]
			if !finite(float64(ax)) || !finite(float64(ay)) || !finite(float64(az)) ||
				!finite(float64(bx)) || !finite(float64(by)) || !finite(float64(bz)) ||
				!finite(float64(cx)) || !finite(float64(cy)) || !finite(float64(cz)) {
				issues.invalidPositions = true
				continue
			}
			ux, uy, uz := float64(bx-ax), float64(by-ay), float64(bz-az)
			vx, vy, vz := float64(cx-ax), float64(cy-ay), float64(cz-az)
			crossX := uy*vz - uz*vy
			crossY := uz*vx - ux*vz
			crossZ := ux*vy - uy*vx
			crossLength := math.Sqrt(crossX*crossX + crossY*crossY + crossZ*crossZ)
			if crossLength == 0 || crossLength < 2*areaEpsilon {
				report.DegenerateTriangles++
				continue
			}
			union.union(wa, wb)
			union.union(wa, wc)
			edges = append(edges,
				edgeKey(wa, wb), edgeKey(wb, wc), edgeKey(wc, wa),
			)
			representatives = append(representatives, wa)
		}
	}

	if len(representatives) == 0 {
		return report, issues
	}

	componentTriangles := make(map[uint32]int64, 16)
	for _, representative := range representatives {
		componentTriangles[union.find(representative)]++
	}
	report.ConnectedComponents = len(componentTriangles)
	largest := int64(0)
	for _, count := range componentTriangles {
		if count > largest {
			largest = count
		}
	}
	total := int64(len(representatives))
	report.LargestComponentRatio = float64(largest) / float64(total)
	if opts.DebrisRatio > 0 {
		for _, count := range componentTriangles {
			if float64(count)/float64(total) < opts.DebrisRatio {
				report.DebrisComponents++
				report.DebrisTriangles += count
			}
		}
	}

	slices.Sort(edges)
	for start := 0; start < len(edges); {
		end := start + 1
		for end < len(edges) && edges[end] == edges[start] {
			end++
		}
		switch used := end - start; {
		case used == 1:
			report.BoundaryEdges++
		case used > 2:
			report.NonManifoldEdges++
		}
		start = end
	}
	// Parts skipped for invalid indices or positions were never counted, so a
	// closed remainder does not prove the whole model is closed.
	report.Watertight = report.BoundaryEdges == 0 && report.NonManifoldEdges == 0 &&
		!issues.invalidIndices && !issues.invalidPositions
	return report, issues
}

// weldKey quantizes a world position to the welding tolerance. Coordinates
// that would overflow the int64 grid fall back to exact float bits, which only
// affects pathologically small tolerances.
func weldKey(point vec3, epsilon float64) [3]int64 {
	var key [3]int64
	for i := 0; i < 3; i++ {
		value := point[i]
		if value == 0 {
			value = 0 // normalize negative zero
		}
		if epsilon > 0 {
			scaled := math.Round(value / epsilon)
			if finite(scaled) && math.Abs(scaled) < 9.0e18 {
				key[i] = int64(scaled)
				continue
			}
		}
		key[i] = int64(math.Float64bits(value))
	}
	return key
}

func edgeKey(a, b uint32) uint64 {
	if a > b {
		a, b = b, a
	}
	return uint64(a)<<32 | uint64(b)
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// unionFind is a small path-compressed, union-by-size structure over welded
// vertex IDs.
type unionFind struct {
	parent []uint32
	size   []uint32
}

func newUnionFind(count int) *unionFind {
	u := &unionFind{
		parent: make([]uint32, count),
		size:   make([]uint32, count),
	}
	for i := range u.parent {
		u.parent[i] = uint32(i)
		u.size[i] = 1
	}
	return u
}

func (u *unionFind) find(value uint32) uint32 {
	root := value
	for u.parent[root] != root {
		root = u.parent[root]
	}
	for u.parent[value] != value {
		next := u.parent[value]
		u.parent[value] = root
		value = next
	}
	return root
}

func (u *unionFind) union(a, b uint32) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if u.size[ra] < u.size[rb] {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
	u.size[ra] += u.size[rb]
}
