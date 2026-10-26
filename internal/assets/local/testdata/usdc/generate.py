"""Optional developer fixture generator. Never called by Go or CI tests."""
import ctypes
import ctypes.util
import json
import os
from pathlib import Path
import struct

os.environ["USD_WRITE_NEW_USDC_FILES_AS_VERSION"] = "0.8.0"
from pxr import Gf, Sdf, Usd, Vt

assert Usd.GetVersion() == (0, 26, 8)
root = Path(__file__).parent
names = ["finite_vec2f", "typed", "local_dependency", "private_text",
         "encoded_text", "nonfinite", "deep_dictionary", "inline_vec4f",
         "compressed_integer_address"]
for name in names:
    target = root / (name + ".usdc")
    target.unlink(missing_ok=True)
    stage = Usd.Stage.CreateNew(str(target))
    prim = stage.DefinePrim("/Probe")
    if name == "finite_vec2f":
        a, b = struct.unpack("<ff", b"\x00//a/\x00\x00\x00")
        prim.CreateAttribute("uv", Sdf.ValueTypeNames.TexCoord2fArray).Set(
            Vt.Vec2fArray([Gf.Vec2f(a, b)]))
    elif name == "typed":
        prim.CreateAttribute("indices", Sdf.ValueTypeNames.IntArray).Set(Vt.IntArray(range(64)))
        prim.CreateAttribute("float", Sdf.ValueTypeNames.Float).Set(1.25)
        prim.CreateAttribute("double", Sdf.ValueTypeNames.Double).Set(1.123456789)
        prim.CreateAttribute("v4", Sdf.ValueTypeNames.Float4).Set(Gf.Vec4f(-1, -1, -1, -1))
        prim.CreateAttribute("flag", Sdf.ValueTypeNames.Bool).Set(True)
        prim.CreateAttribute("s", Sdf.ValueTypeNames.String).Set("public fixture")
        prim.CreateAttribute("normals", Sdf.ValueTypeNames.Normal3fArray).Set(
            Vt.Vec3fArray([Gf.Vec3f(.25, .5, .75)]))
        prim.SetCustomData({"nested": {"enabled": True}})
        prim.SetMetadata("apiSchemas", Sdf.TokenListOp.CreateExplicit(["ProbeAPI"]))
        prim.CreateRelationship("link").SetTargets(["/Probe"])
    elif name == "local_dependency":
        prim.CreateAttribute("texture", Sdf.ValueTypeNames.Asset).Set(Sdf.AssetPath("texture.png"))
    elif name == "private_text":
        prim.CreateAttribute("s", Sdf.ValueTypeNames.String).Set("https://private.invalid/resource")
    elif name == "encoded_text":
        prim.CreateAttribute("s", Sdf.ValueTypeNames.String).Set(
            "%68%74%74%70%73%3a%2f%2fprivate.invalid/resource")
    elif name == "nonfinite":
        prim.CreateAttribute("uv", Sdf.ValueTypeNames.TexCoord2fArray).Set(
            Vt.Vec2fArray([Gf.Vec2f(float("nan"), .25)]))
    elif name == "deep_dictionary":
        nested = True
        for _ in range(40):
            nested = {"nested": nested}
        prim.SetCustomData(nested)
    elif name == "compressed_integer_address":
        payload = b"https://example.invalid/private.gltf".ljust(64, b"\0")
        values = list(struct.unpack("<16i", payload))
        prim.CreateAttribute("payload", Sdf.ValueTypeNames.IntArray).Set(Vt.IntArray(values))
    else:
        prim.SetCustomData({"vector": Gf.Vec4f(47, 47, 97, 47)})
    stage.GetRootLayer().Save()
    del stage
    assert Sdf.Layer.FindOrOpen(str(target)) is not None
    if name == "compressed_integer_address":
        layer = Sdf.Layer.FindOrOpen(str(target))
        assert list(layer.GetAttributeAtPath("/Probe.payload").default) == values
        assert b"https://example.invalid/private.gltf" not in target.read_bytes()

# Independent companion decoding uses the native implementation, not the
# application parser. Small fixtures always use one TfFastCompression block.
lz4 = ctypes.CDLL(ctypes.util.find_library("lz4"))
lz4.LZ4_decompress_safe.argtypes = [ctypes.c_char_p, ctypes.c_void_p,
                                  ctypes.c_int, ctypes.c_int]
def decode(wire, size):
    assert wire[0] == 0
    output = ctypes.create_string_buffer(size)
    assert lz4.LZ4_decompress_safe(wire[1:], output, len(wire)-1, size) == size
    return output.raw

for name in ["finite_vec2f", "typed", "local_dependency"]:
    data = (root / (name + ".usdc")).read_bytes()
    toc = struct.unpack_from("<Q", data, 16)[0]
    sections = {}
    for i in range(6):
        at = toc + 8 + i*32
        sections[data[at:at+16].split(b"\0")[0].decode()] = struct.unpack_from("<QQ", data, at+16)
    at, _ = sections["FIELDS"]
    count, compressed = struct.unpack_from("<QQ", data, at)
    at += 16 + compressed
    size = struct.unpack_from("<Q", data, at)[0]
    reps = list(struct.unpack("<" + "Q"*count, decode(data[at+8:at+8+size], count*8)))
    (root / (name + ".reps.json")).write_text(json.dumps(reps))
    at, _ = sections["TOKENS"]
    count, size, compressed = struct.unpack_from("<QQQ", data, at)
    tokens = [v.decode() for v in decode(data[at+24:at+24+compressed], size).split(b"\0")[:-1]]
    assert len(tokens) == count
    (root / (name + ".tokens.json")).write_text(json.dumps(tokens))
