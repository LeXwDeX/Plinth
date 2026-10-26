package local

import (
	"encoding/binary"
	"errors"
)

// TfFastCompression uses a chunk-count byte followed by LZ4 blocks. The
// bounded decoder below implements that wire contract without a runtime USD
// library. See OpenUSD pxr/base/tf/fastCompression.cpp and sdf/integerCoding.cpp.
func decodeUSDCFast(data []byte, limit int64, work *inspectionWorkBudget) ([]byte, error) {
	if len(data) == 0 || limit < 0 || limit > maxUSDCBytes {
		return nil, errors.New("USDC 压缩大小无效")
	}
	if err := work.reserve(limit); err != nil {
		return nil, err
	}
	return decodeUSDCFastBlocks(data, limit)
}

// Callers reserve the complete decoded bound before entering this decoder.
func decodeUSDCFastBlocks(data []byte, limit int64) ([]byte, error) {
	output := make([]byte, 0, int(limit))
	chunks := int(data[0])
	data = data[1:]
	if chunks == 0 {
		return decodeUSDCLZ4(data, output, int(limit))
	}
	if chunks > 127 {
		return nil, errors.New("USDC 压缩分块无效")
	}
	for range chunks {
		if len(data) < 4 {
			return nil, errors.New("USDC 压缩分块被截断")
		}
		n := uint64(binary.LittleEndian.Uint32(data))
		data = data[4:]
		if n == 0 || n > uint64(len(data)) {
			return nil, errors.New("USDC 压缩分块大小无效")
		}
		var err error
		output, err = decodeUSDCLZ4(data[:n], output, int(limit))
		if err != nil {
			return nil, err
		}
		data = data[n:]
	}
	if len(data) != 0 {
		return nil, errors.New("USDC 压缩分块含尾随字节")
	}
	return output, nil
}

func decodeUSDCLZ4(data, output []byte, limit int) ([]byte, error) {
	base := len(output)
	lastMatch := -1
	length := func(initial int, position *int) (int, error) {
		n := initial
		if initial == 15 {
			for {
				if *position >= len(data) {
					return 0, errors.New("USDC LZ4 长度被截断")
				}
				v := int(data[*position])
				*position++
				if v > limit-n {
					return 0, errors.New("USDC LZ4 展开超出限制")
				}
				n += v
				if v != 255 {
					break
				}
			}
		}
		return n, nil
	}
	for pos := 0; pos < len(data); {
		token := data[pos]
		pos++
		n, err := length(int(token>>4), &pos)
		if err != nil {
			return nil, err
		}
		if n > len(data)-pos || n > limit-len(output) {
			return nil, errors.New("USDC LZ4 字面范围无效")
		}
		output = append(output, data[pos:pos+n]...)
		pos += n
		if pos == len(data) {
			if token&15 != 0 || lastMatch >= 0 && (n < 5 || len(output)-lastMatch < 12) {
				return nil, errors.New("USDC LZ4 末记录无效")
			}
			return output, nil
		}
		if len(data)-pos < 2 {
			return nil, errors.New("USDC LZ4 回引用被截断")
		}
		distance := int(binary.LittleEndian.Uint16(data[pos:]))
		pos += 2
		if distance == 0 || distance > len(output)-base {
			return nil, errors.New("USDC LZ4 回引用无效")
		}
		n, err = length(int(token&15), &pos)
		if err != nil {
			return nil, err
		}
		if n > limit-len(output)-4 {
			return nil, errors.New("USDC LZ4 展开超出限制")
		}
		lastMatch = len(output)
		for range n + 4 {
			output = append(output, output[len(output)-distance])
		}
	}
	return nil, errors.New("USDC LZ4 缺少末记录")
}

// Integer coding stores a common signed delta, two-bit widths and signed
// deltas. Unsigned accumulation deliberately has the wire's uint32 wrap.
func (p *usdcParser) integers(c *usdcCursor, count int64, collect bool) ([]uint32, error) {
	if count < 0 || count > maxUSDCBytes/4 {
		return nil, errors.New("USDC 整数数量超出限制")
	}
	n, err := c.u64()
	if err != nil {
		return nil, err
	}
	body, err := c.take(n)
	if err != nil {
		return nil, err
	}
	limit := int64(0)
	if count != 0 {
		limit = 4 + (count+3)/4 + count*4
	}
	if len(body) == 0 || limit > maxUSDCBytes {
		return nil, errors.New("USDC 压缩大小无效")
	}
	// Delta interpretation, reconstructed LE bytes and the independent encoded
	// address scan are different operations. Prepay them before decompression,
	// including its allocation, instead of reusing the decode reservation.
	decodedBytes := count * 4
	windowBytes := min(decodedBytes, int64(isolationChunkBytes))
	var cost int64
	for _, operation := range []int64{decodedBytes, decodedBytes, windowBytes, limit, limit} {
		if err := addInspectionWork(&cost, operation, 1); err != nil {
			return nil, err
		}
	}
	if err := addInspectionWork(&cost, inspectionCeilChunks(decodedBytes)+inspectionCeilChunks(limit)+2, 8); err != nil {
		return nil, err
	}
	if err := p.work.reserve(cost); err != nil {
		return nil, err
	}
	encoded, err := decodeUSDCFastBlocks(body, limit)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		if len(encoded) != 0 {
			return nil, errors.New("USDC 空整数编码无效")
		}
		return nil, nil
	}
	codeEnd := 4 + int((count+3)/4)
	if len(encoded) < codeEnd {
		return nil, errors.New("USDC 整数编码被截断")
	}
	if count%4 != 0 && encoded[codeEnd-1]>>uint(2*(count%4)) != 0 {
		return nil, errors.New("USDC 整数编码含未使用位")
	}
	encodedScanner := addressScanner{initial: absoluteAddress, continuation: absoluteAddress}
	for offset := 0; offset < len(encoded); offset += isolationChunkBytes {
		end := min(offset+isolationChunkBytes, len(encoded))
		if encodedScanner.contains(encoded[offset:end]) {
			return nil, errors.New("USDC 编码内容含外部地址")
		}
	}
	var values []uint32
	if collect {
		values = make([]uint32, int(count))
	}
	common := binary.LittleEndian.Uint32(encoded)
	previous := uint32(0)
	pos := codeEnd
	window := make([]byte, int(windowBytes))
	used := 0
	valuesScanner := addressScanner{initial: absoluteAddress, continuation: absoluteAddress}
	for i := int64(0); i < count; i++ {
		code := encoded[4+i/4] >> uint(2*(i%4)) & 3
		width := [...]int{0, 1, 2, 4}[code]
		if width > len(encoded)-pos {
			return nil, errors.New("USDC 整数差值被截断")
		}
		delta := common
		switch width {
		case 1:
			delta = uint32(int32(int8(encoded[pos])))
		case 2:
			delta = uint32(int32(int16(binary.LittleEndian.Uint16(encoded[pos:]))))
		case 4:
			delta = binary.LittleEndian.Uint32(encoded[pos:])
		}
		pos += width
		previous += delta
		if collect {
			values[i] = previous
		}
		binary.LittleEndian.PutUint32(window[used:], previous)
		used += 4
		if used == len(window) {
			if valuesScanner.contains(window) {
				return nil, errors.New("USDC 重建整数内容含外部地址")
			}
			used = 0
		}
	}
	if pos != len(encoded) {
		return nil, errors.New("USDC 整数编码含尾随字节")
	}
	if used > 0 && valuesScanner.contains(window[:used]) {
		return nil, errors.New("USDC 重建整数内容含外部地址")
	}
	return values, nil
}
