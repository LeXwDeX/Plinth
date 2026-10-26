package local

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This is a wire safety bound, not product configuration. Version 0.8.0 is
// the explicitly supported subset; another version never falls back to text.
const maxUSDCBytes int64 = 128 << 20

func isUSDCMagic(magic []byte) bool { return bytes.HasPrefix(magic, []byte("PXR-USDC")) }

func isUSDCFormat(magic []byte, name string) bool {
	return isUSDCMagic(magic) || strings.EqualFold(name, "usdc") || strings.HasSuffix(strings.ToLower(name), ".usdc")
}

type usdcCursor struct {
	data     []byte
	pos, end int64
}

func (c *usdcCursor) take(n uint64) ([]byte, error) {
	if c.pos < 0 || c.end > int64(len(c.data)) || c.pos > c.end || n > uint64(c.end-c.pos) {
		return nil, errors.New("USDC 字节范围无效")
	}
	start := c.pos
	c.pos += int64(n)
	return c.data[start:c.pos], nil
}
func (c *usdcCursor) u64() (uint64, error) {
	b, e := c.take(8)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint64(b), nil
}
func (c *usdcCursor) u32() (uint32, error) {
	b, e := c.take(4)
	if e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint32(b), nil
}
func (c *usdcCursor) done() error {
	if c.pos != c.end {
		return errors.New("USDC 记录含尾随字节")
	}
	return nil
}

type usdcSection struct {
	name       string
	start, end int64
}
type usdcClaim struct {
	inspectionSpan
	kind uint8
}
type usdcParser struct {
	data        []byte
	work        *inspectionWorkBudget
	names       map[string]struct{}
	base        string
	tokens      []string
	strings     []uint32
	valueEnd    int64
	pathCount   int64
	pathDefined map[uint32]bool
	reps        []uint64
	plan        inspectionPlan
	claims      []usdcClaim
	seen        map[uint64]bool
	active      map[uint64]bool
	records     int
}

func inspectUSDC(reader io.Reader, size int64, names map[string]struct{}, base string, work *inspectionWorkBudget) error {
	if size < 88 || size > maxUSDCBytes {
		return errors.New("USDC 大小超出检查范围")
	}
	if strings.HasPrefix(base, "/") || strings.ContainsAny(base, "\\%[]\x00") {
		return errors.New("USDC 层路径不支持")
	}
	if work == nil {
		work = newInspectionWorkBudget()
	}
	// Acquire and inspect each bounded wire window in one stage. Do not reuse
	// this prepayment for a later independent traversal of the entire crate.
	if err := work.reserve(size + 8*(inspectionCeilChunks(size)+1)); err != nil {
		return err
	}
	data := make([]byte, int(size))
	if err := readUSDCWire(reader, data); err != nil {
		return err
	}
	p := usdcParser{data: data, work: work, names: names, base: base, seen: map[uint64]bool{}, active: map[uint64]bool{}}
	sections, err := p.bootstrap()
	if err != nil {
		return err
	}
	if err = p.structures(sections); err != nil {
		return err
	}
	for _, rep := range p.reps {
		if err = p.value(rep, 0); err != nil {
			return err
		}
	}
	if err = p.validateClaims(); err != nil {
		return err
	}
	// Counts, indexes, relative pointers and nested ValueReps are also known
	// typed representations. Their referenced text/values were checked above.
	// Leave every unclaimed byte in the ordinary metadata scanner.
	for _, claim := range p.claims {
		p.plan.numeric = append(p.plan.numeric, claim.inspectionSpan)
	}
	if err = normalizeInspectionPlan(&p.plan, size); err != nil {
		return err
	}
	return p.inspectRelativeAddresses()
}

// The wire scanner sees every received byte, before any format interpretation
// can suppress a relative address. Its carry survives every read boundary.
func readUSDCWire(reader io.Reader, data []byte) error {
	absolute := addressScanner{initial: absoluteAddress, continuation: absoluteAddress}
	for offset := 0; offset < len(data); offset += isolationChunkBytes {
		end := min(offset+isolationChunkBytes, len(data))
		count, err := io.ReadFull(reader, data[offset:end])
		if count > 0 && absolute.contains(data[offset:offset+count]) {
			return errors.New("USDC 内容含外部地址")
		}
		if err != nil {
			return errors.New("USDC 内容被截断")
		}
	}
	return nil
}

// The relative scanner sees each unclaimed byte and the same bounded space
// context as a complete mask. Absolute bytes were inspected during acquisition.
// Complete structure and leaf validation precedes this reduction; an unknown
// value, gap or overlapping claim cannot become a numeric boundary.
func (p *usdcParser) inspectRelativeAddresses() error {
	var cost int64
	position := int64(0)
	for _, span := range p.plan.numeric {
		if err := addInspectionWork(&cost, span.start-position+int64(len(typedRunBoundaryBytes(span.end-span.start))), 1); err != nil {
			return err
		}
		position = span.end
	}
	if err := addInspectionWork(&cost, int64(len(p.data))-position, 1); err != nil {
		return err
	}
	if err := addInspectionWork(&cost, inspectionCeilChunks(int64(len(p.data)))+int64(len(p.plan.numeric))*2+1, 8); err != nil {
		return err
	}
	if err := p.work.reserve(cost); err != nil {
		return err
	}
	external := addressScanner{initial: externalAddress, continuation: continuedExternalAddress}
	scan := func(scanner *addressScanner, data []byte) error {
		for len(data) > 0 {
			count := min(len(data), isolationChunkBytes)
			if scanner.contains(data[:count]) {
				return errors.New("USDC 内容含外部地址")
			}
			data = data[count:]
		}
		return nil
	}
	position = 0
	for _, span := range p.plan.numeric {
		if err := scan(&external, p.data[position:span.start]); err != nil {
			return err
		}
		boundary := typedRunBoundaryBytes(span.end - span.start)
		if err := scan(&external, boundary); err != nil {
			return err
		}
		// Keep URI start/continuation decisions identical to the full mask.
		external.seen += span.end - span.start - int64(len(boundary))
		position = span.end
	}
	return scan(&external, p.data[position:])
}

func (p *usdcParser) bootstrap() ([]usdcSection, error) {
	d := p.data
	if !isUSDCMagic(d) || !bytes.Equal(d[8:16], []byte{0, 8, 0, 0, 0, 0, 0, 0}) {
		return nil, errors.New("USDC 版本或标识不支持")
	}
	if !allZeroBytes(d[24:88]) {
		return nil, errors.New("USDC 引导保留字段无效")
	}
	toc := binary.LittleEndian.Uint64(d[16:24])
	if toc < 88 || toc > uint64(len(d))-8 {
		return nil, errors.New("USDC 目录位置无效")
	}
	c := usdcCursor{d, int64(toc), int64(len(d))}
	n, e := c.u64()
	if e != nil {
		return nil, e
	}
	if n != 6 || uint64(c.end-c.pos) != n*32 {
		return nil, errors.New("USDC 目录数量或大小无效")
	}
	known := map[string]bool{"TOKENS": true, "STRINGS": true, "FIELDS": true, "FIELDSETS": true, "PATHS": true, "SPECS": true}
	sections := make([]usdcSection, 0, 6)
	for range n {
		b, e := c.take(16)
		if e != nil {
			return nil, e
		}
		zero := bytes.IndexByte(b, 0)
		if zero < 1 || !allZeroBytes(b[zero:]) {
			return nil, errors.New("USDC 节名称无效")
		}
		name := string(b[:zero])
		if !known[name] {
			return nil, errors.New("USDC 节未知或重复")
		}
		delete(known, name)
		start, e := c.u64()
		if e != nil {
			return nil, e
		}
		length, e := c.u64()
		if e != nil {
			return nil, e
		}
		if start < 88 || start > toc || length > toc-start {
			return nil, errors.New("USDC 节范围无效")
		}
		sections = append(sections, usdcSection{name, int64(start), int64(start + length)})
	}
	sort.Slice(sections, func(i, j int) bool { return sections[i].start < sections[j].start })
	p.valueEnd = sections[0].start
	pos := p.valueEnd
	for _, s := range sections {
		if s.start != pos {
			return nil, errors.New("USDC 结构节重叠或含间隙")
		}
		pos = s.end
	}
	if pos != int64(toc) {
		return nil, errors.New("USDC 目录前含未声明字节")
	}
	return sections, nil
}

func allZeroBytes(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func (p *usdcParser) text(token uint32) (string, error) {
	if uint64(token) >= uint64(len(p.tokens)) {
		return "", errors.New("USDC 文本索引无效")
	}
	return p.tokens[token], nil
}

func (p *usdcParser) stringToken(index uint32) (string, error) {
	if uint64(index) >= uint64(len(p.strings)) {
		return "", errors.New("USDC 字符串索引无效")
	}
	return p.text(p.strings[index])
}

func (p *usdcParser) claim(start, end int64, kind uint8) error {
	if start < 88 || end < start || end > p.valueEnd || len(p.claims) >= maxArchiveFiles {
		return errors.New("USDC 值范围或数量无效")
	}
	if start != end {
		p.claims = append(p.claims, usdcClaim{inspectionSpan{start, end}, kind})
	}
	return nil
}

func (p *usdcParser) validateClaims() error {
	sort.Slice(p.claims, func(i, j int) bool {
		a, b := p.claims[i], p.claims[j]
		if a.start != b.start {
			return a.start < b.start
		}
		return a.end < b.end
	})
	var previous *usdcClaim
	for i := range p.claims {
		c := &p.claims[i]
		if previous != nil && c.start < previous.end {
			if *c == *previous {
				continue
			}
			return errors.New("USDC 值范围重叠")
		}
		previous = c
	}
	return nil
}

func (p *usdcParser) readTokens(c *usdcCursor) error {
	n, e := c.u64()
	if e != nil {
		return e
	}
	u, e := c.u64()
	if e != nil {
		return e
	}
	compressed, e := c.u64()
	if e != nil {
		return e
	}
	if n > maxArchiveFiles || u > maxModelJSONBytes || n > u {
		return errors.New("USDC 文本数量或大小超出限制")
	}
	body, e := c.take(compressed)
	if e != nil {
		return e
	}
	if len(body) == 0 {
		return errors.New("USDC 压缩大小无效")
	}
	// Prepay every declared allocation before decoding. SplitN cannot create
	// more descriptors than the bounded declaration, even for all-NUL data.
	wordBytes := int64(strconv.IntSize / 8)
	var cost int64
	for _, allocation := range []struct{ count, width int64 }{
		{int64(u), 2},                 // Decoded output and the existing text scan.
		{int64(u - n), 1},             // Maximum copied text, excluding terminators.
		{int64(n + 1), 3 * wordBytes}, // []byte descriptors.
		{int64(n), 2 * wordBytes},     // []string storage.
		{int64(n), 4},                 // Bounded token visits.
	} {
		if e = addInspectionWork(&cost, allocation.count, allocation.width); e != nil {
			return e
		}
	}
	if e = p.work.reserve(cost); e != nil {
		return e
	}
	decoded, e := decodeUSDCFastBlocks(body, int64(u))
	if e != nil {
		return e
	}
	if uint64(len(decoded)) != u {
		return errors.New("USDC 文本展开大小不一致")
	}
	parts := bytes.SplitN(decoded, []byte{0}, int(n)+1)
	if len(parts) != int(n)+1 || len(parts[len(parts)-1]) != 0 {
		return errors.New("USDC 文本计数或结束符无效")
	}
	p.tokens = make([]string, int(n))
	for i, part := range parts[:len(parts)-1] {
		if !utf8.Valid(part) || containsExternalAddress(part) {
			return errors.New("USDC 解码文本无效或含外部地址")
		}
		p.tokens[i] = string(part)
	}
	return c.done()
}
