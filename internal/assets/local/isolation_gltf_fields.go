package local

import "errors"

// Only declared accessor storage is numeric. Gaps, unused bufferViews and
// unreferenced bytes receive the complete address check.
func gltfNumericFields(value any, bufferIndex, bufferSize int64) ([]inspectionField, error) {
	document, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("glTF JSON 顶层必须是对象")
	}
	accessors, err := gltfArray(document, "accessors")
	if err != nil {
		return nil, err
	}
	views, err := gltfArray(document, "bufferViews")
	if err != nil {
		return nil, err
	}
	var fields []inspectionField
	for _, value := range accessors {
		accessor, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("glTF 访问器无效")
		}
		if _, present := accessor["bufferView"]; !present {
			continue // Sparse/extension-only storage receives no waiver.
		}
		index, err := gltfInteger(accessor, "bufferView", -1)
		if err != nil || index < 0 || index >= int64(len(views)) {
			return nil, errors.New("glTF 访问器索引无效")
		}
		view, ok := views[index].(map[string]any)
		if !ok {
			return nil, errors.New("glTF 缓冲视图无效")
		}
		index, err = gltfInteger(view, "buffer", -1)
		if err != nil || index < 0 {
			return nil, errors.New("glTF 缓冲视图索引无效")
		}
		if index != bufferIndex {
			continue
		}
		start, err := gltfInteger(view, "byteOffset", 0)
		if err != nil {
			return nil, err
		}
		length, err := gltfInteger(view, "byteLength", -1)
		if err != nil || length <= 0 || start > bufferSize || length > bufferSize-start {
			return nil, errors.New("glTF 缓冲视图范围越界")
		}
		component, err := gltfInteger(accessor, "componentType", -1)
		if err != nil {
			return nil, err
		}
		componentWidth := int64(0)
		switch component {
		case 5120, 5121:
			componentWidth = 1
		case 5122, 5123:
			componentWidth = 2
		case 5125, 5126:
			componentWidth = 4
		default:
			return nil, errors.New("glTF 访问器数值类型无效")
		}
		rows, columns := int64(0), int64(1)
		switch accessor["type"] {
		case "SCALAR":
			rows = 1
		case "VEC2":
			rows = 2
		case "VEC3":
			rows = 3
		case "VEC4":
			rows = 4
		case "MAT2":
			rows, columns = 2, 2
		case "MAT3":
			rows, columns = 3, 3
		case "MAT4":
			rows, columns = 4, 4
		default:
			return nil, errors.New("glTF 访问器形状无效")
		}
		columnWidth := rows * componentWidth
		if columns > 1 {
			columnWidth = (columnWidth + 3) / 4 * 4
		}
		width := columnWidth * columns
		stride, err := gltfInteger(view, "byteStride", width)
		if err != nil || stride < width || stride > 252 || stride%componentWidth != 0 {
			return nil, errors.New("glTF 访问器步长无效")
		}
		if _, present := view["byteStride"]; present && stride%4 != 0 {
			return nil, errors.New("glTF 缓冲视图步长未对齐")
		}
		offset, err := gltfInteger(accessor, "byteOffset", 0)
		if err != nil || offset > length || width > length-offset || (start+offset)%componentWidth != 0 {
			return nil, errors.New("glTF 访问器偏移无效")
		}
		count, err := gltfInteger(accessor, "count", -1)
		if err != nil || count <= 0 || count-1 > (length-offset-width)/stride {
			return nil, errors.New("glTF 访问器数量无效")
		}
		if len(fields) >= maxArchiveFiles {
			return nil, errors.New("glTF 数值字段数量超出限制")
		}
		fields = append(fields, inspectionField{start + offset, width, stride, count, component == 5126})
	}
	return fields, nil
}
