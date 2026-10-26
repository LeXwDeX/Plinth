package local

import (
	"errors"
	"time"
)

const (
	// Work units cover each byte masked, each floating byte validated, four
	// units per record visit, and each interval-index descriptor visit. These
	// are safety bounds, not product settings or elapsed-time thresholds.
	maxInspectionWork   int64 = 512 << 20
	baseInspectionWork  int64 = 1 << 20
	inspectionWorkRatio int64 = 32
	// A delivered file may spend this many units per byte. Every check costs
	// a fixed number of units per byte, so the bound stays linear in the file.
	inspectionWorkPerByte int64 = 8
)

type inspectionWorkBudget struct {
	remaining int64
	// Full image decoding is best-effort: it runs only while the shared budget
	// covers it, and a later mandatory structural check may reclaim what it
	// spent instead of rejecting a file whose structural work fits the budget.
	// Encoding order must not decide whether a valid model is delivered.
	reclaimable int64
	// Every Draco range in one nested asset shares the same absolute deadline.
	dracoDeadline time.Time
}

func newInspectionWorkBudget() *inspectionWorkBudget {
	return &inspectionWorkBudget{remaining: maxInspectionWork}
}

// newInspectionWorkBudgetForSize scales the floor with the delivered file.
// A model can hold several 8K textures whose structure walks alone exceed
// the fixed floor (decided by Lex, 2026-10-10).
func newInspectionWorkBudgetForSize(size int64) *inspectionWorkBudget {
	budget := newInspectionWorkBudget()
	size = min(size, maxArchiveBytes)
	if size > 0 && size*inspectionWorkPerByte > budget.remaining {
		budget.remaining = size * inspectionWorkPerByte
	}
	return budget
}

func inspectionWorkError() error { return errors.New("内容数值检查工作量超出限制") }

// reserve charges mandatory structural work. If the remaining budget is short,
// it first reclaims units spent on optional image decodes: those checks may be
// skipped anywhere, so they must never turn a file that fits the structural
// bound into a rejection. Total work stays bounded by the budget plus the
// decode work it allowed, at most twice the budget.
func (budget *inspectionWorkBudget) reserve(work int64) error {
	if work < 0 {
		return inspectionWorkError()
	}
	if work <= budget.remaining {
		budget.remaining -= work
		return nil
	}
	shortfall := work - budget.remaining
	if shortfall > budget.reclaimable {
		return inspectionWorkError()
	}
	budget.reclaimable -= shortfall
	budget.remaining = 0
	return nil
}

// available is the work a mandatory reservation can cover: what is left plus
// optional decode that reserve may reclaim. Callers gate early rejection (for
// example before allocating an image buffer) on this total, never on
// remaining alone, or optional decode would decide whether a valid file is
// read at all.
func (budget *inspectionWorkBudget) available() int64 {
	return budget.remaining + budget.reclaimable
}

type inspectionWorkEstimate struct{ total, nodes int64 }

type inspectionWorkReservation struct {
	owner    *inspectionWorkBudget
	estimate inspectionWorkEstimate
	nodes    int64
	started  bool
}

// A balanced max-end index prunes fields that ended before this input window.
// Sorting just by start, or scanning every descriptor per chunk, cannot prune
// many short fields after one long overlapping field. The index has at most
// 32768 int64 nodes for the existing 10000-descriptor wire limit.
type inspectionFieldIndex struct {
	ends   []int64
	leaves int
}

func inspectionCeilChunks(size int64) int64 {
	chunks := size / isolationChunkBytes
	if size%isolationChunkBytes != 0 {
		chunks++
	}
	return chunks
}

func addInspectionWork(total *int64, count, unit int64) error {
	if count < 0 || unit <= 0 || count > (maxInspectionWork-*total)/unit {
		return inspectionWorkError()
	}
	*total += count * unit
	return nil
}

func estimateInspectionWork(fields []inspectionField, size, sections int64, inputFields int) (inspectionWorkEstimate, error) {
	if len(fields) == 0 {
		return inspectionWorkEstimate{}, nil
	}
	if sections == 0 {
		sections = 1
	}
	if sections < 0 || sections > maxArchiveFiles+1 {
		return inspectionWorkEstimate{}, inspectionWorkError()
	}
	var extent int64
	for _, field := range fields {
		extent = max(extent, field.end()) // Every field was overflow-checked first.
	}
	if size == 1<<63-1 {
		// Streaming callers without a known size must not derive a budget from
		// MaxInt64. The declared extent bounds work before the first read.
		size = extent
	}
	depth, leaves := int64(0), 1
	for leaves < len(fields) {
		leaves *= 2
		depth++
	}
	var estimate inspectionWorkEstimate
	// Input validation, sorting and index construction remain bounded even
	// when many descriptors canonicalize to one field.
	if err := addInspectionWork(&estimate.total, int64(inputFields), 2*(depth+15)); err != nil {
		return inspectionWorkEstimate{}, err
	}
	if err := addInspectionWork(&estimate.total, int64(2*leaves), 1); err != nil {
		return inspectionWorkEstimate{}, err
	}
	chunkVisits := inspectionCeilChunks(size)
	if err := addInspectionWork(&chunkVisits, sections, 1); err != nil {
		return inspectionWorkEstimate{}, err
	}
	for _, field := range fields {
		// width*count cannot exceed its already validated envelope because
		// stride>=width. Still use division before the work multiplication.
		weight := int64(1)
		if field.floating {
			weight++
		}
		if field.count > maxInspectionWork/field.width {
			return inspectionWorkEstimate{}, inspectionWorkError()
		}
		if err := addInspectionWork(&estimate.total, field.count*field.width, weight); err != nil {
			return inspectionWorkEstimate{}, err
		}
		// An envelope can intersect ceil(envelope/64KiB)+1 fixed-size
		// windows. Image ranges cannot split it: image/numeric overlap was
		// rejected by the parser before this plan was constructed.
		touches := inspectionCeilChunks(field.end()-field.start) + 1
		if err := addInspectionWork(&estimate.total, field.count, 4); err != nil {
			return inspectionWorkEstimate{}, err
		}
		if err := addInspectionWork(&estimate.total, touches, 4); err != nil {
			return inspectionWorkEstimate{}, err
		}
		if err := addInspectionWork(&chunkVisits, touches, 1); err != nil {
			return inspectionWorkEstimate{}, err
		}
	}
	// An interval query visits at most two root-to-leaf paths per active
	// field, plus two paths for an empty window. Charge that entire bound,
	// including gaps and the extra stream sections around embedded images.
	if err := addInspectionWork(&estimate.nodes, chunkVisits, 2*(depth+1)); err != nil {
		return inspectionWorkEstimate{}, err
	}
	if err := addInspectionWork(&estimate.total, estimate.nodes, 1); err != nil {
		return inspectionWorkEstimate{}, err
	}
	limit := maxInspectionWork
	if size <= (maxInspectionWork-baseInspectionWork)/inspectionWorkRatio {
		limit = baseInspectionWork + inspectionWorkRatio*size
	}
	if estimate.total > limit {
		return inspectionWorkEstimate{}, inspectionWorkError()
	}
	return estimate, nil
}

func newInspectionFieldIndex(fields []inspectionField) *inspectionFieldIndex {
	if len(fields) == 0 {
		return nil
	}
	leaves := 1
	for leaves < len(fields) {
		leaves *= 2
	}
	index := &inspectionFieldIndex{ends: make([]int64, 2*leaves), leaves: leaves}
	for position, field := range fields {
		index.ends[leaves+position] = field.end()
	}
	for position := leaves - 1; position > 0; position-- {
		index.ends[position] = max(index.ends[2*position], index.ends[2*position+1])
	}
	return index
}

func (index *inspectionFieldIndex) collect(fields []inspectionField, reservation *inspectionWorkReservation, output []int, offset, end int64) ([]int, error) {
	if offset >= index.ends[1] || end <= fields[0].start {
		return output, nil
	}
	var visit func(node, low, high int) error
	visit = func(node, low, high int) error {
		if reservation.nodes <= 0 {
			return inspectionWorkError()
		}
		reservation.nodes--
		if low >= len(fields) || index.ends[node] <= offset || fields[low].start >= end {
			return nil
		}
		if high-low == 1 {
			output = append(output, low)
			return nil
		}
		middle := low + (high-low)/2
		if err := visit(2*node, low, middle); err != nil {
			return err
		}
		return visit(2*node+1, middle, high)
	}
	err := visit(1, 0, index.leaves)
	return output, err
}
