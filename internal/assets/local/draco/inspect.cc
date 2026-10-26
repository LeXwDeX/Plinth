// Copyright 2026 Art AI contributors. SPDX-License-Identifier: Apache-2.0
// No filesystem, environment or network interface is used. The WASI host only
// supplies the bounded binary request on stdin, scans decoded numeric chunks
// through a read-only import, and accepts an 8-byte receipt.
#include <algorithm>
#include <array>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <limits>
#include <vector>

#include "draco/compression/decode.h"
#include "draco/strict_inspect.h"
namespace draco {
uint32_t art_points = 0, art_faces = 0, art_attributes = 0;
}
namespace {
void Require(bool ok) {
  if (!ok)
    std::abort();
}
uint32_t U32() {
  unsigned char b[4];
  Require(std::fread(b, 1, 4, stdin) == 4);
  return uint32_t(b[0]) | (uint32_t(b[1]) << 8) | (uint32_t(b[2]) << 16) |
         (uint32_t(b[3]) << 24);
}
double F64() {
  uint64_t n = U32();
  n |= uint64_t(U32()) << 32;
  double v;
  std::memcpy(&v, &n, 8);
  Require(std::isfinite(v));
  return v;
}
struct Binding {
  uint32_t id, type, components, normalized, bounds;
  std::array<double, 4> min, max;
};
extern "C" __attribute__((import_module("art_ai"),
                          import_name("scan_numeric"))) uint32_t
ScanNumeric(const void *data, uint32_t size);
// Keep one bounded stream across attributes, mappings and indices. The host
// applies exactly the existing Go absolute-address scanner, including escaped
// forms, UTF-16 projection and overlap; no second decoder lives in this module.
class AbsoluteScanner {
  std::array<unsigned char, 64 * 1024> pending{};
  size_t count = 0;

public:
  void Flush() {
    if (count == 0)
      return;
    Require(ScanNumeric(pending.data(), uint32_t(count)) == 1);
    count = 0;
  }
  void Add(const void *data, size_t size) {
    auto *b = static_cast<const unsigned char *>(data);
    while (size > 0) {
      size_t n = std::min(size, pending.size() - count);
      std::memcpy(pending.data() + count, b, n);
      count += n;
      b += n;
      size -= n;
      if (count == pending.size())
        Flush();
    }
  }
};
template <class T> double Number(const uint8_t *p) {
  T v;
  std::memcpy(&v, p, sizeof(v));
  return double(v);
}
struct Type {
  int draco, width;
};
Type Component(uint32_t t) {
  switch (t) {
  case 5120:
    return {draco::DT_INT8, 1};
  case 5121:
    return {draco::DT_UINT8, 1};
  case 5122:
    return {draco::DT_INT16, 2};
  case 5123:
    return {draco::DT_UINT16, 2};
  case 5125:
    return {draco::DT_UINT32, 4};
  case 5126:
    return {draco::DT_FLOAT32, 4};
  default:
    std::abort();
  }
}
double Value(const uint8_t *p, uint32_t t) {
  switch (t) {
  case 5120:
    return Number<int8_t>(p);
  case 5121:
    return Number<uint8_t>(p);
  case 5122:
    return Number<int16_t>(p);
  case 5123:
    return Number<uint16_t>(p);
  case 5125:
    return Number<uint32_t>(p);
  case 5126:
    return Number<float>(p);
  default:
    std::abort();
  }
}
} // namespace
int main() {
  Require(U32() == 0x31435244); // DRC1, little endian.
  const uint32_t length = U32();
  draco::art_points = U32();
  draco::art_faces = U32();
  draco::art_attributes = U32();
  const uint32_t index_type = U32();
  Require(length >= 12 && length <= 64u * 1024 * 1024 &&
          draco::art_points > 0 && draco::art_points <= 10000000 &&
          draco::art_faces > 0 && draco::art_faces <= 10000000 &&
          draco::art_attributes > 0 && draco::art_attributes <= 16);
  Require(index_type == 5121 || index_type == 5123 || index_type == 5125);
  const uint32_t index_bounds = U32();
  const double index_min = F64(), index_maximum = F64();
  Require(index_bounds <= 3);
  std::vector<Binding> bindings(draco::art_attributes);
  for (auto &b : bindings) {
    b.id = U32();
    b.type = U32();
    b.components = U32();
    b.normalized = U32();
    b.bounds = U32();
    Require(b.components > 0 && b.components <= 4 && b.normalized <= 1 &&
            b.bounds <= 3);
    Component(b.type);
    for (unsigned c = 0; c < b.components; c++) {
      b.min[c] = F64();
      b.max[c] = F64();
    }
  }
  std::vector<char> bytes(length);
  Require(std::fread(bytes.data(), 1, length, stdin) == length &&
          std::fgetc(stdin) == EOF);
  // Only the audited glTF 2.2 Edgebreaker standard/valence subset. No metadata,
  // point clouds, old bitstream paths or unrecognized flags get a numeric span.
  Require(std::memcmp(bytes.data(), "DRACO", 5) == 0 && bytes[5] == 2 &&
          bytes[6] == 2 && bytes[7] == 1 && bytes[8] == 1 && bytes[9] == 0 &&
          bytes[10] == 0 && (bytes[11] == 0 || bytes[11] == 2));
  draco::DecoderBuffer buffer;
  buffer.Init(bytes.data(), bytes.size());
  draco::Decoder decoder;
  auto result = decoder.DecodeMeshFromBuffer(&buffer);
  Require(result.ok() && buffer.remaining_size() == 0);
  auto mesh = std::move(result).value();
  Require(mesh->num_points() == draco::art_points &&
          mesh->num_faces() == draco::art_faces &&
          mesh->num_attributes() == int(draco::art_attributes) &&
          mesh->GetMetadata() == nullptr);
  for (int i = 0; i < mesh->num_attributes(); i++)
    for (int j = 0; j < i; j++)
      Require(mesh->attribute(i)->unique_id() !=
              mesh->attribute(j)->unique_id());
  for (size_t i = 0; i < bindings.size(); i++)
    for (size_t j = 0; j < i; j++)
      Require(bindings[i].id != bindings[j].id);
  AbsoluteScanner scan;
  for (const auto &b : bindings) {
    auto *a = mesh->GetAttributeByUniqueId(b.id);
    auto type = Component(b.type);
    Require(a && a->data_type() == type.draco &&
            a->num_components() == b.components &&
            a->normalized() == bool(b.normalized) && a->size() > 0 &&
            a->size() <= draco::art_points);
    const size_t width = size_t(type.width) * b.components;
    Require(a->byte_stride() == int64_t(width) && a->byte_offset() == 0 &&
            a->buffer()->data_size() == width * a->size());
    std::array<double, 4> lo, hi;
    lo.fill(std::numeric_limits<double>::infinity());
    hi.fill(-std::numeric_limits<double>::infinity());
    for (draco::AttributeValueIndex i(0); i.value() < a->size(); ++i) {
      const auto *p = a->GetAddress(i);
      scan.Add(p, width);
      for (unsigned c = 0; c < b.components; c++) {
        double v = Value(p + c * type.width, b.type);
        Require(std::isfinite(v));
        lo[c] = std::min(lo[c], v);
        hi[c] = std::max(hi[c], v);
      }
    }
    for (unsigned c = 0; c < b.components; c++) {
      if (b.bounds & 1)
        Require(lo[c] == b.min[c]);
      if (b.bounds & 2)
        Require(hi[c] == b.max[c]);
    }
    for (draco::PointIndex p(0); p.value() < mesh->num_points(); ++p) {
      uint32_t index = a->mapped_index(p).value();
      Require(index < a->size());
      scan.Add(&index, 4);
    }
  }
  const uint32_t index_max = index_type == 5121   ? 255
                             : index_type == 5123 ? 65535
                                                  : 0xffffffffu;
  uint32_t actual_min = 0xffffffffu, actual_max = 0;
  for (draco::FaceIndex f(0); f.value() < mesh->num_faces(); ++f)
    for (auto p : mesh->face(f)) {
      uint32_t index = p.value();
      Require(index < mesh->num_points() && index <= index_max);
      scan.Add(&index, 4);
      actual_min = std::min(actual_min, index);
      actual_max = std::max(actual_max, index);
    }
  if (index_bounds & 1)
    Require(actual_min == index_min);
  if (index_bounds & 2)
    Require(actual_max == index_maximum);
  scan.Flush();
  Require(std::fwrite("DRC1OK\r\n", 1, 8, stdout) == 8);
  return 0;
}
