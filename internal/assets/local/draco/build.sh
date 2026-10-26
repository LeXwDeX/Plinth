#!/bin/sh
# Rebuild the checked-in WASM. SDK and CMake are build-time tools only.
set -eu
: "${WASI_SDK_PATH:?Set WASI_SDK_PATH to the official WASI SDK 34.0 directory}"
: "${CMAKE:=cmake}"
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/art-ai-draco.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
curl -fL 'https://github.com/google/draco/archive/refs/tags/1.5.7.tar.gz' -o "$work/source.tar.gz"
python3 - "$work/source.tar.gz" <<'PY'
import hashlib,sys
expected='bf6b105b79223eab2b86795363dfe5e5356050006a96521477973aba8f036fe1'
if hashlib.sha256(open(sys.argv[1],'rb').read()).hexdigest()!=expected:raise SystemExit('Draco source digest mismatch')
PY
tar -xzf "$work/source.tar.gz" -C "$work"
patch -d "$work/draco-1.5.7" -p1 < "$root/strict.patch"
"$CMAKE" -S "$work/draco-1.5.7" -B "$work/build" \
 -DCMAKE_BUILD_TYPE=Release -DDRACO_GLTF_BITSTREAM=ON -DDRACO_TESTS=OFF \
 -DCMAKE_TOOLCHAIN_FILE="$WASI_SDK_PATH/share/cmake/wasi-sdk-p1.cmake" \
 '-DCMAKE_CXX_FLAGS=-include algorithm -include iterator -fno-exceptions'
"$CMAKE" --build "$work/build" --target draco_static -j 4
"$WASI_SDK_PATH/bin/clang++" --target=wasm32-wasip1 -O2 -DNDEBUG -std=c++17 -fno-exceptions \
 -I"$work/draco-1.5.7/src" -I"$work/build" "$root/inspect.cc" "$work/build/libdraco.a" \
 -Wl,-z,stack-size=1048576 -Wl,--max-memory=1073741824 -Wl,--strip-all -o "$work/inspect.wasm"
python3 - "$root" "$work/inspect.wasm" <<'PY'
import hashlib,json,pathlib,sys
root=pathlib.Path(sys.argv[1]);manifest=json.loads((root/'manifest.json').read_text())
for name in ['inspect.cc','strict.patch']:
 if hashlib.sha256((root/name).read_bytes()).hexdigest()!=manifest['files'][name]:raise SystemExit(name+' source digest mismatch')
wasm=pathlib.Path(sys.argv[2]).read_bytes()
if hashlib.sha256(wasm).hexdigest()!=manifest['files']['inspect.wasm']:raise SystemExit('Rebuilt WASM digest differs; review toolchain/source before replacing manifest')
(root/'inspect.wasm').write_bytes(wasm)
print('Rebuilt WASM matches the reviewed manifest')
PY
