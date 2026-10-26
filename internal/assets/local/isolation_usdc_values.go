package local

import (
	"errors"
	"math"
	"net/url"
	"path"
	"strings"
)

func (p *usdcParser) value(rep uint64, depth int) error {
	if depth > 32 || p.records >= maxArchiveFiles {
		return errors.New("USDC 值深度或数量超出限制")
	}
	if p.active[rep] {
		return errors.New("USDC 值含循环引用")
	}
	if p.seen[rep] {
		return nil
	}
	p.records++
	if e := p.work.reserve(32); e != nil {
		return e
	}
	typ := uint8(rep >> 48)
	array := rep>>63 != 0
	inline := rep&(1<<62) != 0
	compressed := rep&(1<<61) != 0
	payload := rep & ((1 << 48) - 1)
	if rep&(uint64(0x1f)<<56) != 0 || inline && array || compressed && !array {
		return errors.New("USDC 值标志不支持")
	}
	// Upstream files are cached as delivered: a value this walker does not
	// decode (list ops, time samples, compressed 64-bit or float arrays, ...)
	// stays opaque. Its bytes remain unclaimed, so the ordinary metadata
	// scanner still reads them.
	if !supportedUSDCType(typ) || array && !usdcNumericType(typ) || compressed && typ != 3 && typ != 4 {
		p.seen[rep] = true
		return nil
	}
	if inline {
		if payload > math.MaxUint32 {
			return errors.New("USDC 内联值超出范围")
		}
		v := uint32(payload)
		switch typ {
		case 1:
			if v > 1 {
				return errors.New("USDC 布尔值无效")
			}
		case 3, 20, 24, 28: // Integers and vector components have finite int encodings.
		case 8, 9: // IEEE values, including NaN/Infinity, are delivered unchanged.
		case 10:
			if _, e := p.stringToken(v); e != nil {
				return e
			}
		case 11:
			if _, e := p.text(v); e != nil {
				return e
			}
		case 12:
			text, e := p.text(v)
			if e != nil {
				return e
			}
			if !safeUSDCAsset(text, p.names, p.base) {
				return errors.New("USDC 资产依赖没有本地文件")
			}
		case 31:
			if v != 0 {
				return errors.New("USDC 内联字典无效")
			}
		case 42:
			if v > 2 {
				return errors.New("USDC 定义类型值无效")
			}
		case 44:
			if v > 2 {
				return errors.New("USDC 可变性值无效")
			}
		default: // Opaque inline values, see above.
		}
		p.seen[rep] = true
		return nil
	}
	if array && payload == 0 {
		if compressed {
			return errors.New("USDC 空数组标志无效")
		}
		p.seen[rep] = true
		return nil
	}
	if payload < 88 || payload >= uint64(p.valueEnd) {
		return errors.New("USDC 值位置无效")
	}
	c := usdcCursor{p.data, int64(payload), p.valueEnd}
	p.active[rep] = true
	defer delete(p.active, rep)
	var err error
	if array {
		err = p.numericArray(&c, typ, compressed)
	} else {
		switch {
		case usdcNumericType(typ):
			err = p.numeric(&c, 1, typ)
		case typ == 31:
			err = p.dictionary(&c, depth+1)
		case typ == 41:
			err = p.indexVector(&c, false)
		case typ == 32 || typ == 34:
			err = p.list(&c, typ == 34)
		default: // Opaque out-of-line values, see above.
		}
	}
	if err == nil {
		p.seen[rep] = true
	}
	return err
}

// USD asset paths are resolver paths, not glTF URL paths. This subset accepts
// plain relative member names and rejects percent/package syntax rather than
// claiming a URL-decoded name is the file USD would actually resolve.
func safeUSDCAsset(text string, names map[string]struct{}, base string) bool {
	if text == "" || names == nil || strings.HasPrefix(text, "/") || strings.ContainsAny(text, "\\%[]\x00") || containsExternalAddress([]byte(text)) {
		return false
	}
	parsed, e := url.Parse(text)
	if e != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	// These dependencies have complete archive-member inspectors. Other USD
	// resolver formats (including textual layers or packages) are unsupported.
	switch strings.ToLower(path.Ext(text)) {
	case ".png", ".jpg", ".jpeg", ".usdc":
	default:
		return false
	}
	name := path.Clean(path.Join(path.Dir(base), text))
	if name == ".." || strings.HasPrefix(name, "../") {
		return false
	}
	_, exists := names[name]
	return exists
}

// usdcNumericWidth gives the byte width of one fixed-size USD crate value
// (TypeEnum in crateDataTypes.h) and the width of its IEEE float word, 0 when
// it carries no float32/float64 words (integers, booleans, half floats).
func usdcNumericWidth(typ uint8) (width, word int64, ok bool) {
	switch typ {
	case 1, 2: // Bool, UChar
		return 1, 0, true
	case 3, 4: // Int, UInt
		return 4, 0, true
	case 5, 6: // Int64, UInt64
		return 8, 0, true
	case 7: // Half
		return 2, 0, true
	case 8: // Float
		return 4, 4, true
	case 9, 56: // Double, TimeCode
		return 8, 8, true
	case 13, 14, 15: // Matrix2d, Matrix3d, Matrix4d
		side := int64(typ - 11)
		return side * side * 8, 8, true
	case 16, 19, 23, 27: // Quatd, Vec2d, Vec3d, Vec4d
		return map[uint8]int64{16: 32, 19: 16, 23: 24, 27: 32}[typ], 8, true
	case 17, 20, 24, 28: // Quatf, Vec2f, Vec3f, Vec4f
		return map[uint8]int64{17: 16, 20: 8, 24: 12, 28: 16}[typ], 4, true
	case 18, 21, 25, 29: // Quath, Vec2h, Vec3h, Vec4h
		return map[uint8]int64{18: 8, 21: 4, 25: 6, 29: 8}[typ], 0, true
	case 22, 26, 30: // Vec2i, Vec3i, Vec4i
		return map[uint8]int64{22: 8, 26: 12, 30: 16}[typ], 0, true
	}
	return 0, 0, false
}

func usdcNumericType(typ uint8) bool {
	_, _, ok := usdcNumericWidth(typ)
	return ok
}

func supportedUSDCType(typ uint8) bool {
	switch typ {
	case 10, 11, 12, 31, 32, 34, 41, 42, 44:
		return true
	}
	return usdcNumericType(typ)
}

func (p *usdcParser) numericArray(c *usdcCursor, typ uint8, compressed bool) error {
	if !usdcNumericType(typ) {
		return errors.New("USDC 数组类型不支持")
	}
	start := c.pos
	n, e := c.u64()
	if e != nil {
		return e
	}
	if e = p.claim(start, c.pos, 1); e != nil {
		return e
	}
	if n > uint64(maxUSDCBytes/4) {
		return errors.New("USDC 数组数量超出限制")
	}
	if compressed {
		if typ != 3 && typ != 4 || n < 16 {
			return errors.New("USDC 压缩数组类型或数量无效")
		}
		start = c.pos
		if _, e = p.integers(c, int64(n), false); e != nil {
			return e
		}
		if e = p.claim(start, c.pos, 0x83); e != nil {
			return e
		}
		p.plan.numeric = append(p.plan.numeric, inspectionSpan{start, c.pos})
		return nil
	}
	return p.numeric(c, int64(n), typ)
}

func (p *usdcParser) numeric(c *usdcCursor, count int64, typ uint8) error {
	width, word, ok := usdcNumericWidth(typ)
	if !ok {
		return errors.New("USDC 数值类型不支持")
	}
	if count < 0 || count > maxUSDCBytes/width {
		return errors.New("USDC 数值数组大小无效")
	}
	n := count * width
	start := c.pos
	_, e := c.take(uint64(n))
	if e != nil {
		return e
	}
	if e = p.claim(start, c.pos, typ); e != nil {
		return e
	}
	if word != 0 {
		// USDC delivery checks structure, not numerical rendering quality.
		// Keep the previous conservative work charge without the shared finite
		// validator: other formats must retain their finite-value contract.
		if e = p.work.reserve(n/word*2 + (inspectionCeilChunks(n)+1)*8); e != nil {
			return e
		}
	}
	p.plan.numeric = append(p.plan.numeric, inspectionSpan{start, c.pos})
	return nil
}

func (p *usdcParser) indexVector(c *usdcCursor, paths bool) error {
	start := c.pos
	n, e := c.u64()
	if e != nil {
		return e
	}
	if n > maxArchiveFiles {
		return errors.New("USDC 引用向量数量超出限制")
	}
	if n > uint64(c.end-c.pos)/4 {
		return errors.New("USDC 引用向量被截断")
	}
	// Overlapping malicious vectors can revisit the same bytes many times.
	// Charge their reads and index visits before walking, not only once when
	// the final non-overlapping mask plan is reserved.
	if e = p.work.reserve(int64(n) * 8); e != nil {
		return e
	}
	for range n {
		v, e := c.u32()
		if e != nil {
			return e
		}
		if paths {
			if !p.pathDefined[v] {
				return errors.New("USDC 路径引用无效")
			}
		} else if _, e = p.text(v); e != nil {
			return e
		}
	}
	kind := uint8(41)
	if paths {
		kind = 40
	}
	return p.claim(start, c.pos, kind)
}

func (p *usdcParser) list(c *usdcCursor, paths bool) error {
	start := c.pos
	header, e := c.take(1)
	if e != nil {
		return e
	}
	flags := header[0]
	if flags&0x80 != 0 || flags&1 != 0 && flags&0x7c != 0 {
		return errors.New("USDC 列表标志无效")
	}
	kind := uint8(32)
	if paths {
		kind = 34
	}
	if e = p.claim(start, c.pos, kind); e != nil {
		return e
	}
	// Wire order: explicit, added, prepended, appended, deleted, ordered.
	for _, bit := range []byte{2, 4, 32, 64, 8, 16} {
		if flags&bit != 0 {
			if e = p.indexVector(c, paths); e != nil {
				return e
			}
		}
	}
	return nil
}

func (p *usdcParser) dictionary(c *usdcCursor, depth int) error {
	start := c.pos
	n, e := c.u64()
	if e != nil {
		return e
	}
	if n > maxArchiveFiles {
		return errors.New("USDC 字典数量超出限制")
	}
	if e = p.claim(start, c.pos, 31); e != nil {
		return e
	}
	keys := map[string]bool{}
	for range n {
		start = c.pos
		key, e := c.u32()
		if e != nil {
			return e
		}
		keyText, keyErr := p.stringToken(key)
		if keyErr != nil {
			return keyErr
		}
		if keys[keyText] {
			return errors.New("USDC 字典键重复")
		}
		keys[keyText] = true
		jumpStart := c.pos
		offset, e := c.u64()
		if e != nil {
			return e
		}
		if offset < 8 || offset > uint64(c.end-jumpStart-8) {
			return errors.New("USDC 字典相对位置无效")
		}
		if e = p.claim(start, c.pos, 31); e != nil {
			return e
		}
		c.pos = jumpStart + int64(offset)
		repStart := c.pos
		rep, e := c.u64()
		if e != nil {
			return e
		}
		if e = p.claim(repStart, c.pos, 52); e != nil {
			return e
		}
		if e = p.value(rep, depth); e != nil {
			return e
		}
	}
	return nil
}
