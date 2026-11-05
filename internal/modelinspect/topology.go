package modelinspect

import (
	"context"
	"math"
	"slices"
)

// topologyIssues records data problems that Inspect reports as warnings.
type topologyIssues struct {
	invalidIndices   bool
	invalidPositions bool
}

// maxWeldedVertices bounds welded vertex IDs so an edge key can hold two IDs
// and a direction bit in 64 bits.
const maxWeldedVertices = 1 << 31

// computeTopology welds positions per the option tolerance, then counts
// connected components and edge use. Edges are collected as 64-bit keys and
// sorted, which keeps memory linear and is faster than a map for the ~2M
// triangle models this package targets (see BenchmarkTopologyEdgeCounting).
//
// Only vertices a triangle references are welded, and strip or fan
// triangles that repeat an index (stitching) are skipped entirely. The pass
// checks ctx regularly and returns ctx.Err() once it is done.
func (m *Model) computeTopology(
	ctx context.Context, analyses []*partAnalysis, worldBox bounds, opts InspectOptions,
) (TopologyReport, topologyIssues, error) {
	// Watertight stays false until every triangle has been analysed and the
	// edge counts prove a closed, consistently oriented surface.
	var report TopologyReport
	var issues topologyIssues
	if !worldBox.valid || len(m.parts) == 0 {
		return report, issues, nil
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
		analysis := analyses[index]
		if analysis == nil || p.mode < 4 || analysis.triangles == 0 {
			continue
		}
		if !analysis.indicesOK {
			issues.invalidIndices = true
			continue
		}
		count := p.vertexCount
		if count < 0 || count > maxIntValue/3 {
			continue
		}
		positions := analysis.positions
		world := make([]float32, count*3)
		ids := make([]uint32, count)
		for vertex := 0; vertex < count; vertex++ {
			if vertex%inspectCheckInterval == 0 {
				if err := ctx.Err(); err != nil {
					return TopologyReport{}, topologyIssues{}, err
				}
			}
			if !analysis.uses(vertex) {
				continue
			}
			point := analysis.pose.point(vertex,
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
				if nextID == maxWeldedVertices {
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
		// A model with more than 2^31 welded vertices is far outside every
		// supported target; report the facts gathered so far rather than
		// allocating a wider key representation.
		issues.invalidIndices = true
		return report, issues, nil
	}

	weldCount := int(nextID)
	if weldCount == 0 {
		return report, issues, nil
	}
	union := newUnionFind(weldCount)
	totalTriangles := int64(0)
	for index := range m.parts {
		if analyses[index] != nil {
			totalTriangles += analyses[index].triangles
		}
	}
	capacity := totalTriangles * 3
	if capacity < 0 || capacity > int64(maxIntValue/8) {
		capacity = 0
	}
	edges := make([]uint64, 0, int(capacity))
	representatives := make([]uint32, 0, int(minInt64(capacity/3, int64(maxIntValue))))
	// Six times the signed volume the triangles enclose, measured from the
	// box centre: a closed surface encloses the same volume from any point,
	// and a nearby one keeps the float error small.
	origin := worldBox.center()
	signedVolume := 0.0

	checked := 0
	for index, p := range m.parts {
		ids := vertexIDs[index]
		world := worldPositions[index]
		if ids == nil {
			continue
		}
		analysis := analyses[index]
		mirrored := analysis.pose.mirrored
		count := p.partTriangles(analysis.indices)
		for triangle := 0; triangle < count; triangle++ {
			checked++
			if checked%inspectCheckInterval == 0 {
				if err := ctx.Err(); err != nil {
					return TopologyReport{}, topologyIssues{}, err
				}
			}
			a, b, c, ok := p.triangle(triangle, analysis.indices)
			if !ok {
				continue
			}
			if p.mode != 4 && (a == b || b == c || a == c) {
				// Strip stitching or restart, not a face.
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
			pa := vec3{float64(ax) - origin[0], float64(ay) - origin[1], float64(az) - origin[2]}
			pb := vec3{float64(bx) - origin[0], float64(by) - origin[1], float64(bz) - origin[2]}
			pc := vec3{float64(cx) - origin[0], float64(cy) - origin[1], float64(cz) - origin[2]}
			if mirrored {
				// A mirroring transform reverses the winding glTF assigns to
				// the front face, so the world-space orientation is flipped.
				wb, wc = wc, wb
				pb, pc = pc, pb
			}
			signedVolume += pa.dot(pb.cross(pc))
			edges = append(edges,
				edgeKey(wa, wb), edgeKey(wb, wc), edgeKey(wc, wa),
			)
			representatives = append(representatives, wa)
		}
	}

	if len(representatives) == 0 {
		return report, issues, nil
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

	if err := ctx.Err(); err != nil {
		return TopologyReport{}, topologyIssues{}, err
	}
	slices.Sort(edges)
	for start := 0; start < len(edges); {
		edge := edges[start] >> 1
		end, forward := start, 0
		for end < len(edges) && edges[end]>>1 == edge {
			if edges[end]&1 == 0 {
				forward++
			}
			end++
		}
		switch used := end - start; {
		case used == 1:
			report.BoundaryEdges++
		case used > 2:
			report.NonManifoldEdges++
		case forward != 1:
			// Both triangles walk the edge the same way: one of them faces
			// the opposite side of the surface.
			report.InconsistentWindingEdges++
		}
		start = end
	}
	// Parts skipped for invalid indices or positions were never counted, so a
	// closed remainder does not prove the whole model is closed.
	closed := report.BoundaryEdges == 0 && report.NonManifoldEdges == 0 &&
		report.InconsistentWindingEdges == 0 &&
		!issues.invalidIndices && !issues.invalidPositions
	// Only a closed, consistently wound surface has a meaningful signed
	// volume; a negative one means every face points inward. Shells nested
	// as cavities subtract from the outer shell, so a hollow model stays
	// positive. The tolerance ignores volumes lost to float rounding.
	diagonal := worldBox.diagonal()
	report.InsideOut = closed && signedVolume/6 < -insideOutTolerance*diagonal*diagonal*diagonal
	report.Watertight = closed && !report.InsideOut
	return report, issues, nil
}

// insideOutTolerance is the share of the bounding-box diagonal cubed below
// which a negative enclosed volume is treated as zero.
const insideOutTolerance = 1e-9

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

// edgeKey packs the edge from -> to so both directions of one undirected
// edge sort next to each other: the smaller ID in bits 33-63, the larger in
// bits 1-32 and bit 0 set when the edge runs from the larger ID to the
// smaller. IDs stay below maxWeldedVertices, so nothing overlaps.
func edgeKey(from, to uint32) uint64 {
	if from < to {
		return uint64(from)<<33 | uint64(to)<<1
	}
	return uint64(to)<<33 | uint64(from)<<1 | 1
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
