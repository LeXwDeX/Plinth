package local

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sort"
)

// inspectionSpan identifies bytes whose interpretation was verified by a wire
// parser. Half-open offsets always refer to the same data passed to inspect.
type inspectionSpan struct {
	start, end int64
}

type inspectionPlan struct {
	// numeric bytes cannot represent a protocol-relative resource reference.
	// Absolute HTTP(S) addresses remain checked in these bytes.
	numeric []inspectionSpan
	fields  []inspectionField
	// floatBytes is zero for the existing float32 plans. Binary FBX can
	// declare float64 fields; their words must never be split into float32s.
	floatBytes int64
	// allowed bytes are exempt from every address check: a fully validated
	// PNG/JPEG cached as delivered, or an inline data URI that was decoded
	// and inspected on its own.
	allowed []inspectionSpan
	// Each stream section can start one additional short 64KiB window.
	sections    int64
	inputFields int
	index       *inspectionFieldIndex
	work        inspectionWorkEstimate
	reservation *inspectionWorkReservation
}

type plannedInspector struct {
	absolute addressScanner
	external addressScanner
	previous []byte
	work     *inspectionWorkBudget
	active   []int
}

func newPlannedInspector() *plannedInspector {
	return newPlannedInspectorWithWork(newInspectionWorkBudget())
}

func newPlannedInspectorWithWork(work *inspectionWorkBudget) *plannedInspector {
	if work == nil {
		work = newInspectionWorkBudget()
	}
	return &plannedInspector{
		absolute: addressScanner{initial: absoluteAddress, continuation: absoluteAddress},
		external: addressScanner{initial: externalAddress, continuation: continuedExternalAddress},
		work:     work,
	}
}

func (inspector *plannedInspector) prepare(plan *inspectionPlan, size int64) error {
	if err := normalizeInspectionPlan(plan, size); err != nil {
		return err
	}
	if len(plan.fields) == 0 {
		return nil
	}
	if plan.reservation != nil && plan.reservation.owner == inspector.work && !plan.reservation.started &&
		plan.reservation.estimate == plan.work {
		return nil // A second preflight is not a second execution.
	}
	if err := inspector.work.reserve(plan.work.total); err != nil {
		return err
	}
	plan.reservation = &inspectionWorkReservation{owner: inspector.work, estimate: plan.work, nodes: plan.work.nodes}
	return nil
}

func (inspector *plannedInspector) inspect(data []byte, plan inspectionPlan) error {
	if err := inspector.prepare(&plan, int64(len(data))); err != nil {
		return err
	}
	for offset := 0; offset < len(data); offset += isolationChunkBytes {
		end := min(offset+isolationChunkBytes, len(data))
		if err := inspector.inspectPart(data[offset:end], plan, int64(offset)); err != nil {
			return err
		}
	}
	return nil
}

func (inspector *plannedInspector) inspectPart(data []byte, plan inspectionPlan, offset int64) error {
	if err := inspector.inspectFinitePart(data, &plan, offset); err != nil {
		return err
	}
	absolute := append([]byte(nil), data...)
	maskInspectionSpans(absolute, offset, plan.allowed)
	if inspector.absolute.contains(absolute) {
		return rejectInspection("external_address", errors.New("内容含外部地址"))
	}
	maskInspectionSpans(absolute, offset, plan.numeric)
	for _, index := range inspector.active {
		maskInspectionField(absolute, offset, plan.fields[index])
	}
	if inspector.external.contains(absolute) {
		return rejectInspection("external_address", errors.New("内容含外部地址"))
	}
	return nil
}

// The FBX parser batches its delivered-wire address scans across small typed
// properties. Keep field/index accounting and finite-word carry independent
// so each local float32/float64 plan is validated before entering that batch.
func (inspector *plannedInspector) inspectFinitePart(data []byte, plan *inspectionPlan, offset int64) error {
	if offset < 0 || int64(len(data)) > math.MaxInt64-offset {
		return errors.New("内容检查流位置无效")
	}
	inspector.active = inspector.active[:0]
	if len(plan.fields) != 0 && len(data) != 0 {
		if plan.reservation == nil || plan.reservation.owner != inspector.work {
			if err := inspector.prepare(plan, math.MaxInt64); err != nil {
				return err
			}
		}
		plan.reservation.started = true
		var err error
		inspector.active, err = plan.index.collect(plan.fields, plan.reservation, inspector.active, offset, offset+int64(len(data)))
		if err != nil {
			return err
		}
	}
	return inspector.validateFloats(data, offset, plan.fields, inspector.active, inspectionFloatBytes(*plan))
}

// Regular accessor storage avoids one allocation and range entry per vertex.
type inspectionField struct {
	start, width, stride, count int64
	floating                    bool
}

func (field inspectionField) end() int64 {
	return field.start + (field.count-1)*field.stride + field.width
}

func maskInspectionField(data []byte, offset int64, field inspectionField) {
	if field.end() <= offset || field.start >= offset+int64(len(data)) {
		return
	}
	first := firstInspectionField(field, offset)
	for index := first; index < field.count; index++ {
		start := field.start + index*field.stride
		if start >= offset+int64(len(data)) {
			break
		}
		for position, end := max(start, offset), min(start+field.width, offset+int64(len(data))); position < end; position++ {
			data[position-offset] = ' '
		}
	}
}

func (inspector *plannedInspector) validateFloats(data []byte, offset int64, fields []inspectionField, active []int, wordBytes int64) error {
	var window []byte
	start := offset - int64(len(inspector.previous))
	for _, index := range active {
		field := fields[index]
		if !field.floating {
			continue
		}
		if window == nil {
			window = make([]byte, len(inspector.previous)+len(data))
			copy(window, inspector.previous)
			copy(window[len(inspector.previous):], data)
		}
		first := firstInspectionField(field, start)
		for index := first; index < field.count; index++ {
			position := field.start + index*field.stride
			if position >= offset+int64(len(data)) {
				break
			}
			component := position
			if component < start {
				words := (start - component) / wordBytes
				if (start-component)%wordBytes != 0 {
					words++
				}
				component += words * wordBytes
			}
			stop := min(position+field.width, offset+int64(len(data)))
			for ; component <= stop-wordBytes; component += wordBytes {
				if component+wordBytes <= offset {
					continue
				}
				var value float64
				if wordBytes == 8 {
					value = math.Float64frombits(binary.LittleEndian.Uint64(window[component-start : component-start+8]))
				} else {
					value = float64(math.Float32frombits(binary.LittleEndian.Uint32(window[component-start : component-start+4])))
				}
				if math.IsNaN(value) || math.IsInf(value, 0) {
					return errors.New("内容数值字段含非有限浮点数")
				}
			}
		}
	}
	// One incomplete float64 word can straddle a stream window. The same
	// seven-byte carry also preserves all existing float32 checks.
	if len(data) >= 7 {
		inspector.previous = append(inspector.previous[:0], data[len(data)-7:]...)
	} else {
		keep := min(len(inspector.previous), 7-len(data))
		inspector.previous = append(inspector.previous[:0], inspector.previous[len(inspector.previous)-keep:]...)
		inspector.previous = append(inspector.previous, data...)
	}
	return nil
}

func firstInspectionField(field inspectionField, offset int64) int64 {
	if offset < field.start+field.width {
		return 0
	}
	return (offset-field.start-field.width)/field.stride + 1
}

func normalizeInspectionPlan(plan *inspectionPlan, size int64) error {
	if size < 0 {
		return errors.New("内容检查大小无效")
	}
	if len(plan.fields) > maxArchiveFiles {
		return errors.New("内容数值字段数量超出限制")
	}
	if plan.floatBytes != 0 && plan.floatBytes != 4 && plan.floatBytes != 8 {
		return errors.New("内容浮点字段宽度无效")
	}
	if err := validateInspectionFieldRangesTyped(plan.fields, size, inspectionFloatBytes(*plan)); err != nil {
		return err
	}
	plan.inputFields = max(plan.inputFields, len(plan.fields))
	for index, field := range plan.fields {
		if field.stride == field.width {
			// Equivalent contiguous SCALAR/VEC accessors share one byte range.
			// The range check above proves this multiplication cannot overflow.
			field.width *= field.count
			field.count, field.stride = 1, field.width
			plan.fields[index] = field
		}
	}
	sort.Slice(plan.fields, func(left, right int) bool {
		a, b := plan.fields[left], plan.fields[right]
		if a.start != b.start {
			return a.start < b.start
		}
		if a.width != b.width {
			return a.width < b.width
		}
		if a.stride != b.stride {
			return a.stride < b.stride
		}
		return a.count < b.count
	})
	fields := plan.fields[:0]
	for _, field := range plan.fields {
		if len(fields) != 0 {
			previous := &fields[len(fields)-1]
			if field.start == previous.start && field.width == previous.width && field.stride == previous.stride && field.count == previous.count {
				previous.floating = previous.floating || field.floating
				continue
			}
		}
		fields = append(fields, field)
	}
	plan.fields = fields
	var err error
	plan.work, err = estimateInspectionWork(plan.fields, size, plan.sections, plan.inputFields)
	if err != nil {
		return err
	}
	plan.index = newInspectionFieldIndex(plan.fields)
	for _, target := range []*[]inspectionSpan{&plan.numeric, &plan.allowed} {
		spans := *target
		if len(spans) > maxArchiveFiles {
			return errors.New("内容检查范围数量超出限制")
		}
		for _, span := range spans {
			if span.start < 0 || span.end < span.start || span.end > size {
				return errors.New("内容检查范围无效")
			}
		}
		sort.Slice(spans, func(left, right int) bool { return spans[left].start < spans[right].start })
		merged := spans[:0]
		for _, span := range spans {
			if span.start == span.end {
				continue
			}
			if len(merged) != 0 && span.start <= merged[len(merged)-1].end {
				merged[len(merged)-1].end = max(merged[len(merged)-1].end, span.end)
			} else {
				merged = append(merged, span)
			}
		}
		*target = merged
	}
	return nil
}

func validateInspectionFieldRanges(fields []inspectionField, size int64) error {
	return validateInspectionFieldRangesTyped(fields, size, 4)
}

func inspectionFloatBytes(plan inspectionPlan) int64 {
	if plan.floatBytes == 8 {
		return 8
	}
	return 4
}

func validateInspectionFieldRangesTyped(fields []inspectionField, size, wordBytes int64) error {
	for _, field := range fields {
		if field.start < 0 || field.width <= 0 || field.count <= 0 || field.stride < field.width || field.start > size ||
			field.width > size-field.start || field.count-1 > (size-field.start-field.width)/field.stride ||
			(field.floating && field.width%wordBytes != 0) {
			return errors.New("内容数值字段范围无效")
		}
	}
	return nil
}

func maskInspectionSpans(data []byte, offset int64, spans []inspectionSpan) {
	end := offset + int64(len(data))
	index := sort.Search(len(spans), func(index int) bool { return spans[index].end > offset })
	for ; index < len(spans) && spans[index].start < end; index++ {
		span := spans[index]
		for start, stop := max(span.start, offset), min(span.end, end); start < stop; start++ {
			data[start-offset] = ' '
		}
	}
}

func inspectPlannedBytes(reader io.Reader, plan inspectionPlan) error {
	return inspectPlannedBytesWithWork(reader, plan, newInspectionWorkBudget())
}

func inspectPlannedBytesWithWork(reader io.Reader, plan inspectionPlan, work *inspectionWorkBudget) error {
	size := int64(math.MaxInt64)
	if sized, ok := reader.(interface{ Len() int }); ok {
		size = int64(sized.Len())
	} else if limited, ok := reader.(*io.LimitedReader); ok {
		size = max(0, limited.N)
	}
	inspector := newPlannedInspectorWithWork(work)
	if err := inspector.prepare(&plan, size); err != nil {
		return err
	}
	buffer := make([]byte, isolationChunkBytes)
	var offset int64
	for {
		// Aggregate short reads so a one-byte Reader cannot multiply field
		// descriptor queries beyond the preflighted 64KiB window count.
		count, err := io.ReadFull(reader, buffer)
		if count > 0 {
			if inspectErr := inspector.inspectPart(buffer[:count], plan, offset); inspectErr != nil {
				return inspectErr
			}
			offset += int64(count)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			if err := validateInspectionFieldRangesTyped(plan.fields, offset, inspectionFloatBytes(plan)); err != nil {
				return err
			}
			for _, spans := range [][]inspectionSpan{plan.numeric, plan.allowed} {
				for _, span := range spans {
					if span.end > offset {
						return errors.New("内容检查范围无效")
					}
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}
