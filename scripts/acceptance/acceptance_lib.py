"""Shared helpers for the local acceptance scripts (mock_stack.py, cross_user.py): a small cookie-aware HTTP client,
PNG encode/decode and multipart upload bodies. Standard library only."""
import json, os, socket, struct, subprocess, sys, time, uuid, zlib, hashlib, urllib.request, urllib.error, http.cookiejar

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k): return None

class Api:
    def __init__(self, base):
        self.base = base.rstrip("/"); self.jar = http.cookiejar.CookieJar()
        self.op = urllib.request.build_opener(NoRedirect(), urllib.request.HTTPCookieProcessor(self.jar))
    def call(self, method, path, body=None, headers=None, raw=None, ctype=None, timeout=60):
        h = {"Origin": self.base, **(headers or {})}
        data = raw
        if body is not None: data = json.dumps(body).encode(); h["Content-Type"] = "application/json"
        elif ctype: h["Content-Type"] = ctype
        req = urllib.request.Request(self.base + path, data=data, method=method, headers=h)
        try: r = self.op.open(req, timeout=timeout)
        except urllib.error.HTTPError as e: r = e
        except Exception as e: return 0, {}, b"", str(e)
        b = r.read(); return (r.status if hasattr(r, "status") else r.code), dict(r.headers), b, ""
    def j(self, *a, **k):
        s, h, b, e = self.call(*a, **k)
        try: d = json.loads(b)
        except Exception: d = {}
        return s, d, h
    def mock_login(self):
        return self.j("POST", "/api/auth/mock", {})

def png(w, h, rgba_fn):
    raw = b"".join(b"\0" + b"".join(bytes(rgba_fn(x, y)) for x in range(w)) for y in range(h))
    def chunk(t, d): c = struct.pack(">I", len(d)) + t + d; return c + struct.pack(">I", zlib.crc32(t + d) & 0xffffffff)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 6, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")

def png_decode(b):
    assert b[:8] == b"\x89PNG\r\n\x1a\n", "not png"
    pos = 8; idat = b""; w = h = ct = bd = il = None
    while pos < len(b):
        n, t = struct.unpack(">I4s", b[pos:pos+8]); d = b[pos+8:pos+8+n]; pos += 12 + n
        if t == b"IHDR": w, h, bd, ct, _, _, il = struct.unpack(">IIBBBBB", d)
        elif t == b"IDAT": idat += d
    return {"w": w, "h": h, "ct": ct, "bd": bd, "il": il, "idat": idat}

def png_pixels(b):
    p = png_decode(b); assert p["bd"] == 8 and p["il"] == 0 and p["ct"] in (2, 6), f"unsupported png {p['ct']}/{p['bd']}/{p['il']}"
    bpp = 4 if p["ct"] == 6 else 3; w, h = p["w"], p["h"]; raw = zlib.decompress(p["idat"]); stride = w * bpp
    rows = []; prev = bytearray(stride); pos = 0
    for _ in range(h):
        f = raw[pos]; line = bytearray(raw[pos+1:pos+1+stride]); pos += 1 + stride
        for i in range(stride):
            a = line[i-bpp] if i >= bpp else 0; up = prev[i]; c = prev[i-bpp] if i >= bpp else 0
            if f == 1: line[i] = (line[i] + a) & 255
            elif f == 2: line[i] = (line[i] + up) & 255
            elif f == 3: line[i] = (line[i] + (a + up) // 2) & 255
            elif f == 4:
                pa, pb, pc = abs(up - c), abs(a - c), abs(a + up - 2 * c)
                pr = a if pa <= pb and pa <= pc else (up if pb <= pc else c)
                line[i] = (line[i] + pr) & 255
        rows.append(line); prev = line
    return w, h, bpp, rows

def multipart(filename, ctype, payload, field="file"):
    bd = "----acc" + uuid.uuid4().hex
    body = (f'--{bd}\r\nContent-Disposition: form-data; name="{field}"; filename="{filename}"\r\nContent-Type: {ctype}\r\n\r\n').encode() + payload + f"\r\n--{bd}--\r\n".encode()
    return body, f"multipart/form-data; boundary={bd}"


def repo_root():
    """The checkout this file lives in (scripts/acceptance/ -> repository root)."""
    return os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))

def free_ports(n):
    """Return n distinct currently-free loopback TCP ports."""
    socks = []; ports = []
    for _ in range(n):
        s = socket.socket(); s.bind(("127.0.0.1", 0)); socks.append(s); ports.append(s.getsockname()[1])
    for s in socks: s.close()
    return ports

def build_binary(repo, out):
    """go build ./cmd/art-ai into out (CGO off, same as the release build)."""
    subprocess.check_call(["go", "build", "-o", out, "./cmd/art-ai"], cwd=repo, env=dict(os.environ, CGO_ENABLED="0"))

def check_web_assets(repo, override=None):
    root = override or os.path.join(repo, "build", "go-web")
    if not os.path.isdir(root):
        sys.exit(f"{root} is missing: run `pnpm build` first (the stack serves the built SPA from it)")
    return root
