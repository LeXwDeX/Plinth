package local

import (
	"encoding/binary"
	"errors"
)

func (p *usdcParser) structures(sections []usdcSection) error {
	byName := make(map[string]usdcSection, 6)
	for _, s := range sections {
		byName[s.name] = s
	}
	cursor := func(name string) *usdcCursor { s := byName[name]; return &usdcCursor{p.data, s.start, s.end} }
	if e := p.readTokens(cursor("TOKENS")); e != nil {
		return e
	}
	c := cursor("STRINGS")
	n, e := c.u64()
	if e != nil {
		return e
	}
	if n > maxArchiveFiles || n > uint64(c.end-c.pos)/4 {
		return errors.New("USDC 字符串表大小无效")
	}
	p.strings = make([]uint32, int(n))
	for i := range p.strings {
		v, e := c.u32()
		if e != nil {
			return e
		}
		if _, e = p.text(v); e != nil {
			return e
		}
		p.strings[i] = v
	}
	if e = c.done(); e != nil {
		return e
	}
	c = cursor("FIELDS")
	n, e = c.u64()
	if e != nil {
		return e
	}
	if n > maxArchiveFiles {
		return errors.New("USDC 字段数量超出限制")
	}
	indexes, e := p.integers(c, int64(n), true)
	if e != nil {
		return e
	}
	for _, v := range indexes {
		if _, e = p.text(v); e != nil {
			return e
		}
	}
	compressed, e := c.u64()
	if e != nil {
		return e
	}
	body, e := c.take(compressed)
	if e != nil {
		return e
	}
	reps, e := decodeUSDCFast(body, int64(n)*8, p.work)
	if e != nil {
		return e
	}
	if uint64(len(reps)) != n*8 {
		return errors.New("USDC 字段展开大小不一致")
	}
	if containsAddress(reps, absoluteAddress) {
		return errors.New("USDC 字段编码含外部地址")
	}
	p.reps = make([]uint64, int(n))
	for i := range p.reps {
		p.reps[i] = binary.LittleEndian.Uint64(reps[i*8:])
	}
	if e = c.done(); e != nil {
		return e
	}
	c = cursor("FIELDSETS")
	n, e = c.u64()
	if e != nil {
		return e
	}
	if n > maxArchiveFiles {
		return errors.New("USDC 字段集数量超出限制")
	}
	sets, e := p.integers(c, int64(n), true)
	if e != nil {
		return e
	}
	starts := map[uint32]bool{0: true}
	for i, v := range sets {
		if v == ^uint32(0) {
			starts[uint32(i+1)] = true
		} else if uint64(v) >= uint64(len(p.reps)) {
			return errors.New("USDC 字段集索引无效")
		}
	}
	if len(sets) > 0 && sets[len(sets)-1] != ^uint32(0) {
		return errors.New("USDC 字段集缺少结束符")
	}
	if e = c.done(); e != nil {
		return e
	}
	if e = p.paths(cursor("PATHS")); e != nil {
		return e
	}
	c = cursor("SPECS")
	n, e = c.u64()
	if e != nil {
		return e
	}
	if n > maxArchiveFiles {
		return errors.New("USDC 定义数量超出限制")
	}
	paths, e := p.integers(c, int64(n), true)
	if e != nil {
		return e
	}
	fields, e := p.integers(c, int64(n), true)
	if e != nil {
		return e
	}
	types, e := p.integers(c, int64(n), true)
	if e != nil {
		return e
	}
	seen := make(map[uint32]bool, len(paths))
	for i, v := range paths {
		if !p.pathDefined[v] || seen[v] || !starts[fields[i]] || uint64(fields[i]) >= uint64(len(sets)) || types[i] < 1 || types[i] > 11 {
			return errors.New("USDC 定义索引或类型无效")
		}
		seen[v] = true
	}
	if e = c.done(); e != nil {
		return e
	}
	// Every byte in these spans has now been decoded as a known structural
	// representation; decoded text was scanned before the wire interpretation.
	for _, s := range sections {
		p.plan.numeric = append(p.plan.numeric, inspectionSpan{s.start, s.end})
	}
	return nil
}

func (p *usdcParser) paths(c *usdcCursor) error {
	total, e := c.u64()
	if e != nil {
		return e
	}
	n, e := c.u64()
	if e != nil {
		return e
	}
	if total > maxArchiveFiles || n > total {
		return errors.New("USDC 路径数量无效")
	}
	indexes, e := p.integers(c, int64(n), true)
	if e != nil {
		return e
	}
	tokens, e := p.integers(c, int64(n), true)
	if e != nil {
		return e
	}
	jumps, e := p.integers(c, int64(n), true)
	if e != nil {
		return e
	}
	seenIndex := map[uint32]bool{}
	for i, v := range indexes {
		if uint64(v) >= total || seenIndex[v] {
			return errors.New("USDC 路径索引无效或重复")
		}
		seenIndex[v] = true
		t := int64(int32(tokens[i]))
		if t < 0 {
			t = -t
		}
		if t >= int64(len(p.tokens)) {
			return errors.New("USDC 路径文本索引无效")
		}
	}
	// Follow the compressed tree with a bounded stack. A node may be visited
	// once; jumps cannot cycle, overlap or leave undeclared encoded nodes.
	stack := []int{0}
	visited := make([]bool, int(n))
	count := 0
	if n == 0 {
		stack = nil
	}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if i < 0 || i >= len(visited) || visited[i] {
			return errors.New("USDC 路径树重叠或越界")
		}
		visited[i] = true
		count++
		jump := int64(int32(jumps[i]))
		if jump < -2 {
			return errors.New("USDC 路径树跳转无效")
		}
		child := jump > 0 || jump == -1
		sibling := jump >= 0
		if child {
			stack = append(stack, i+1)
		}
		if sibling {
			next := int64(i) + 1
			if child {
				next = int64(i) + jump
			}
			if next >= int64(n) {
				return errors.New("USDC 路径兄弟跳转越界")
			}
			stack = append(stack, int(next))
		}
	}
	if count != int(n) {
		return errors.New("USDC 路径树含未访问节点")
	}
	p.pathCount = int64(total)
	p.pathDefined = seenIndex
	return c.done()
}
