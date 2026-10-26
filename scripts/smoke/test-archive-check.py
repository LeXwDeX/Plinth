#!/usr/bin/env python3
"""Offline archive parser and bounded HTTP-read regression tests."""
import http.server
import importlib.util
import io
import json
import os
import pathlib
import struct
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import zipfile

sys.dont_write_bytecode = True
HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("archive_check", HERE / "check-archive.py")
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class Unseekable(io.BytesIO):
    def seek(self, *args):
        raise io.UnsupportedOperation("unseekable")


def archive(method=zipfile.ZIP_DEFLATED, descriptor=False, name="README.txt",
            text="已包含的格式：\n- GLB：model.glb\n".encode()):
    output = Unseekable() if descriptor else io.BytesIO()
    with zipfile.ZipFile(output, "w", compression=method) as z:
        z.writestr(name, text)
        z.writestr("other.glb" if name == "model.glb" else "model.glb", b"model")
    return output.getvalue()


class ArchiveTests(unittest.TestCase):
    def test_good_stored_deflated_and_data_descriptors(self):
        for method in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED):
            for descriptor in (False, True):
                with self.subTest(method=method, descriptor=descriptor):
                    data = archive(method, descriptor)
                    # Remove the central directory: only local data is required.
                    checker.check_prefix(data[:data.index(b"PK\x01\x02")])
                    if descriptor:
                        self.assertEqual(struct.unpack_from("<I", data, 18)[0], 0)

    def test_wrong_first_entry(self):
        with self.assertRaisesRegex(ValueError, "first entry"):
            checker.check_prefix(archive(name="model.glb"))

    def test_provider_addresses(self):
        for address in (b"http://provider.example", b"HTTPS://provider.example"):
            for method in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED):
                for descriptor in (False, True):
                    with self.subTest(address=address, method=method, descriptor=descriptor):
                        with self.assertRaisesRegex(ValueError, "http\\(s\\) address"):
                            checker.check_prefix(archive(method, descriptor, text=address))

    def test_malformed_truncated_crc_and_unsupported(self):
        good = archive()
        bad_crc = bytearray(good)
        bad_crc[14] ^= 1
        unsupported = bytearray(good)
        unsupported[8] = 9
        for data in (b"not zip", good[:25], good[:42], bytes(bad_crc), bytes(unsupported)):
            with self.subTest(data=data[:30]):
                with self.assertRaises((ValueError, checker.zlib.error)):
                    checker.check_prefix(data)

    def test_incomplete_descriptor_and_oversized_readme(self):
        data = archive(descriptor=True)
        with self.assertRaisesRegex(ValueError, "descriptor"):
            checker.check_prefix(data[:data.index(b"PK\x07\x08") + 5])
        with self.assertRaisesRegex(ValueError, "4 MiB"):
            checker.check_prefix(archive(text=b"a" * (checker.MAX_README_BYTES + 1)))

    def test_layout_keeps_single_files_at_the_root(self):
        # Only the OBJ package may use a folder (#222).
        with self.assertRaisesRegex(ValueError, "may keep a folder"):
            checker.check_layout("已包含的格式：\n- GLB：glb/model.glb\n".encode())
        # A referenced but unsaved dependency does not justify a folder either.
        with self.assertRaisesRegex(ValueError, "lone model file"):
            checker.check_layout(
                "已包含的格式：\n- OBJ：obj/model.obj\n\nOBJ 的材质与贴图（与 OBJ 放在同一文件夹）：\n"
                "- obj/scene/temp.mtl（OBJ 引用了该文件，但任务没有保存它）\n".encode()
            )
        # An OBJ that ships its material library keeps its folder.
        checker.check_layout(
            "已包含的格式：\n- OBJ：obj/model.obj\n\nOBJ 的材质与贴图（与 OBJ 放在同一文件夹）：\n- obj/model.mtl\n".encode()
        )
        # Every model in a folder needs a packed sibling, not only a lone one.
        with self.assertRaisesRegex(ValueError, "lone model file"):
            checker.check_layout(
                "已包含的格式：\n- GLB：model.glb\n- OBJ：obj/model.obj\n".encode()
            )
        # The full ZIP checks the entry list as well as the README.
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "archive.zip"
            with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as z:
                z.writestr("README.txt", "已包含的格式：\n- GLB：glb/model.glb\n".encode())
                z.writestr("glb/model.glb", b"model")
            with self.assertRaisesRegex(ValueError, "may keep a folder"):
                checker.check_full(path)

    def test_layout_allows_other_and_dependency_only_archives(self):
        # other/ is the declared home of attachments that match no format.
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "archive.zip"
            with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as z:
                z.writestr("README.txt", "其他文件：\n- other/model.blend.bin\n".encode())
                z.writestr("other/model.blend.bin", b"blend")
            checker.check_full(path)
            # A dependency may be all that could be read; it is not a model.
            with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as z:
                z.writestr("README.txt", (
                    "已包含的格式：\n（没有任何格式）\n\n"
                    "OBJ 的材质与贴图（与 OBJ 放在同一文件夹）：\n- obj/model.mtl\n").encode())
                z.writestr("obj/model.mtl", b"newmtl surface")
            checker.check_full(path)

    def test_layout_reads_dependency_paths_verbatim(self):
        # A legal file name may contain full-width punctuation; the section,
        # not the punctuation, says what the line is.
        readme = (
            "已包含的格式：\n- OBJ：obj/model.obj\n\n"
            "OBJ 的材质与贴图（与 OBJ 放在同一文件夹）：\n"
            "- obj/材质（1）.mtl\n- obj/贴图：颜色.png\n"
        ).encode()
        checker.check_layout(readme)
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "archive.zip"
            with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as z:
                z.writestr("README.txt", readme)
                for name in ("obj/model.obj", "obj/材质（1）.mtl", "obj/贴图：颜色.png"):
                    z.writestr(name, b"x")
            checker.check_full(path)
            # A missing-reference note still names an absent file, and the
            # note text is the only thing that makes it a reference.
            with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as z:
                z.writestr("README.txt", readme + "- obj/材质（2）.mtl".encode() + checker.MISSING_REFERENCE_NOTE.encode())
                for name in ("obj/model.obj", "obj/材质（1）.mtl", "obj/贴图：颜色.png"):
                    z.writestr(name, b"x")
            checker.check_full(path)

    def test_layout_keeps_the_obj_package_together(self):
        # An OBJ that has a packed dependency uses its folder (#222).
        for dependency in ("obj/model.mtl", "model.mtl"):
            with self.subTest(dependency=dependency):
                with self.assertRaisesRegex(ValueError, "must keep its folder"):
                    checker.check_layout((
                        "已包含的格式：\n- OBJ：model.obj\n\n"
                        "OBJ 的材质与贴图（与 OBJ 放在同一文件夹）：\n- %s\n" % dependency).encode())
        # A dependency belongs inside the OBJ's folder, not somewhere else.
        with self.assertRaisesRegex(ValueError, "not inside its OBJ folder"):
            checker.check_layout((
                "已包含的格式：\n- OBJ：obj/model.obj\n\n"
                "OBJ 的材质与贴图（与 OBJ 放在同一文件夹）：\n- elsewhere/model.mtl\n").encode())
        # Nested references from the material library are still inside it.
        checker.check_layout((
            "已包含的格式：\n- OBJ：obj/model.obj\n\n"
            "OBJ 的材质与贴图（与 OBJ 放在同一文件夹）：\n- obj/model.mtl\n- obj/textures/base.png\n").encode())
        # Unmatched attachments live under other/, never beside the model.
        with self.assertRaisesRegex(ValueError, "outside other/"):
            checker.check_layout("其他文件：\n- stuff/model.blend.bin\n".encode())
        with self.assertRaisesRegex(ValueError, "outside other/"):
            checker.check_layout((
                "已包含的格式：\n- OBJ：obj/model.obj\n\n"
                "OBJ 的材质与贴图（与 OBJ 放在同一文件夹）：\n- obj/model.mtl\n\n"
                "其他文件：\n- obj/extra.bin\n").encode())

    def test_layout_requires_listed_dependencies_to_be_packed(self):
        # A dependency the README lists as shipped must be a real entry.
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "archive.zip"
            with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as z:
                z.writestr("README.txt", (
                    "已包含的格式：\n- OBJ：obj/model.obj\n\n"
                    "OBJ 的材质与贴图（与 OBJ 放在同一文件夹）：\n- obj/ghost.mtl\n").encode())
                z.writestr("obj/model.obj", b"mtllib ghost.mtl")
            with self.assertRaisesRegex(ValueError, "not in the archive"):
                checker.check_full(path)
            # An unincluded format line names a reason, not a path.
            checker.check_layout(
                "已包含的格式：\n- OBJ：model.obj\n\n未包含的格式：\n- FBX：上游没有返回该格式\n".encode())

    def test_constants_match_the_product_schema(self):
        catalog = json.loads((HERE.parents[1] / "internal/catalog/default_catalog.json").read_text())
        readme = catalog["application"]["model_delivery"]["archive"]["readme"]
        self.assertEqual(checker.INCLUDED_HEADING, readme["included_heading"])
        self.assertEqual(checker.MISSING_HEADING, readme["missing_heading"])
        self.assertEqual(checker.DEPENDENCY_HEADING, readme["dependency_heading"])
        self.assertEqual(checker.OTHER_HEADING, readme["other_heading"])
        self.assertEqual(
            checker.MISSING_REFERENCE_NOTE,
            readme["dependency_missing_line"].replace("- {path}", ""),
        )
        self.assertTrue(readme["included_line"].startswith("- {label}："))
        self.assertTrue(readme["included_line"].endswith("：{path}"))
        # The parser treats every dependency/other line as "- {path}" alone.
        self.assertEqual(readme["dependency_line"], "- {path}")
        self.assertEqual(readme["other_line"], "- {path}")
        schema = catalog["application"]["model_delivery"]
        obj = next(f for f in schema["formats"] if f["id"] == "obj")
        self.assertEqual(checker.OBJ_LABEL, obj["label"])
        self.assertEqual(checker.OTHER_DIRECTORY, schema["archive"]["other_directory"])

    def test_layout_rejects_unsafe_and_unknown_readme_paths(self):
        for path in ("../model.glb", "/etc/model.glb", "glb\\model.glb", "C:model.glb", "glb//model.glb"):
            with self.subTest(path=path):
                with self.assertRaisesRegex(ValueError, "unsafe archive path"):
                    checker.check_layout(f"已包含的格式：\n- GLB：{path}\n".encode())
        # The full ZIP proves every path listed as included is a real entry.
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "archive.zip"
            with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as z:
                z.writestr("README.txt", "已包含的格式：\n- GLB：missing.glb\n".encode())
                z.writestr("model.glb", b"model")
            with self.assertRaisesRegex(ValueError, "not in the archive"):
                checker.check_full(path)

    def test_http_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            hdr = pathlib.Path(directory) / "headers"
            for header, expected in (
                ("HTTP/1.1 200 OK\r\nContent-Type: application/zip\r\n\r\n", (200, None)),
                ("HTTP/1.1 200 OK\r\nContent-Type: application/zip; charset=binary\r\nContent-Length: 100\r\n\r\n", (200, 100)),
                ("HTTP/1.1 206 OK\r\nContent-Type: application/zip\r\nContent-Length: 50\r\nContent-Range: bytes 0-49/100\r\n\r\n", (206, 100)),
                ("HTTP/1.1 206 OK\r\nContent-Type: application/zip\r\nContent-Range: bytes 0-49/*\r\n\r\n", (206, None)),
            ):
                hdr.write_text("HTTP/1.1 200 Connection established\r\n\r\n" + header)
                self.assertEqual(checker.response_metadata(hdr), expected)
            for header in (
                "HTTP/1.1 404 Not Found\r\nContent-Type: application/zip\r\n\r\n",
                "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n",
                "HTTP/1.1 206 OK\r\nContent-Type: application/zip\r\nContent-Range: bytes 1-49/100\r\n\r\n",
            ):
                hdr.write_text(header)
                with self.assertRaises(ValueError):
                    checker.response_metadata(hdr)

    def test_full_zip_check(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "archive.zip"
            path.write_bytes(archive())
            self.assertIn("2 entries", checker.check_full(path))
            for data in (archive(name="model.glb"), archive(text=b"http://provider.example"), archive()[:-25]):
                path.write_bytes(data)
                with self.assertRaises((ValueError, zipfile.BadZipFile)):
                    checker.check_full(path)

    def test_default_partial_deadline(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "cookies").touch()
            (root / "fixture.zip").write_bytes(archive())
            fake_curl = root / "curl"
            fake_curl.write_text(f"#!{sys.executable}\n" + '''
import json, pathlib, sys
root = pathlib.Path(__file__).parent
args = sys.argv[1:]
(root / "curl-args.json").write_text(json.dumps(args))
body = (root / "fixture.zip").read_bytes()
pathlib.Path(args[args.index("-D") + 1]).write_text(
    f"HTTP/1.1 200 OK\\r\\nContent-Type: application/zip\\r\\nContent-Length: {len(body)}\\r\\n\\r\\n")
sys.stdout.buffer.write(body)
''')
            fake_curl.chmod(0o700)
            env = os.environ.copy()
            env.pop("ARCHIVE_PARTIAL_TIMEOUT", None)
            env["PATH"] = directory + os.pathsep + env["PATH"]
            result = subprocess.run(["bash", str(HERE / "read-archive.sh"),
                "http://127.0.0.1/archive", str(root / "cookies"), directory],
                env=env, capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            args = json.loads((root / "curl-args.json").read_text())
            self.assertEqual(args[args.index("--max-time") + 1], "59")
            self.assertEqual(args[args.index("--range") + 1], "0-4194303")

    def test_stalled_preflight_and_incomplete_prefix_fail(self):
        small = archive(descriptor=True)

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                if self.path == "/partial-body":
                    self.send_response(200)
                    self.send_header("Content-Type", "application/zip")
                    self.end_headers()
                    self.wfile.write(small)
                    self.wfile.flush()
                # Model preflight can delay all headers/bytes. A valid README
                # received before a stalled remainder must also remain FAIL.
                time.sleep(0.25)

            def log_message(self, *args):
                pass

        with http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler) as server:
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                for route in ("no-bytes", "partial-body"):
                    with self.subTest(route=route), tempfile.TemporaryDirectory() as directory:
                        root = pathlib.Path(directory)
                        (root / "cookies").touch()
                        env = os.environ.copy()
                        env["ARCHIVE_PARTIAL_TIMEOUT"] = "0.1"
                        result = subprocess.run(["bash", str(HERE / "read-archive.sh"),
                            f"http://127.0.0.1:{server.server_port}/{route}", str(root / "cookies"), directory],
                            env=env, capture_output=True, text=True, timeout=5)
                        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                        self.assertIn("curl: (28)", result.stderr)
                        size = (root / "archive-prefix.zip").stat().st_size
                        self.assertEqual(size, 0 if route == "no-bytes" else len(small))
                        self.assertNotIn("partial check only", result.stdout)
            finally:
                server.shutdown()
                thread.join()

    def test_bounded_http_reads(self):
        small = archive(descriptor=True)
        output = io.BytesIO()
        with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_STORED) as z:
            z.writestr("README.txt", "已包含的格式：\n- GLB：model.glb\n".encode())
            z.writestr("model.glb", b"x" * (5 * 1024 * 1024))
        medium = output.getvalue()
        # The first entry is valid; the remaining body simulates a 355 MiB ZIP.
        large_size = 355 * 1024 * 1024
        prefix = small + b"x" * 65536
        requests = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                requests.append((self.path, self.headers.get("Range")))
                ranged = self.path in ("/range", "/range-small", "/range-full-fetch") and self.headers.get("Range")
                small_body = self.path in ("/small", "/range-small", "/unknown-small", "/corrupt-full", "/range-full-fetch")
                body = medium if self.path == "/range-full-fetch" else small[:-25] if self.path == "/corrupt-full" else small
                size = len(body) if small_body else large_size
                length = min(size, 4194304) if ranged else size
                self.send_response(206 if ranged else 200)
                self.send_header("Content-Type", "text/html" if self.path == "/bad-mime" else "application/zip")
                if self.path not in ("/unknown", "/unknown-small"):
                    self.send_header("Content-Length", str(length))
                if ranged:
                    self.send_header("Content-Range", f"bytes 0-{length-1}/{size}")
                self.end_headers()
                try:
                    if small_body:
                        self.wfile.write(body[:length])
                    else:
                        remaining = length
                        while remaining:
                            chunk = prefix[:min(remaining, len(prefix))]
                            self.wfile.write(chunk)
                            remaining -= len(chunk)
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def log_message(self, *args):
                pass

        with http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler) as server:
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                for route in ("range", "ignore-range", "unknown", "small", "range-small", "range-full-fetch", "unknown-small", "bad-mime", "corrupt-full"):
                    with self.subTest(route=route), tempfile.TemporaryDirectory() as directory:
                        root = pathlib.Path(directory)
                        (root / "cookies").touch()
                        result = subprocess.run(["bash", str(HERE / "read-archive.sh"),
                            f"http://127.0.0.1:{server.server_port}/{route}", str(root / "cookies"), directory],
                            capture_output=True, text=True, timeout=15)
                        self.assertEqual(result.returncode, 1 if route in ("bad-mime", "corrupt-full") else 0,
                            result.stdout + result.stderr)
                        self.assertLessEqual((root / "archive-prefix.zip").stat().st_size, 4194304)
                        if route in ("small", "range-small", "range-full-fetch"):
                            self.assertIn("full ZIP check", result.stdout)
                            if route == "range-full-fetch":
                                self.assertEqual((root / "archive.zip").stat().st_size, len(medium))
                                self.assertIn(("/range-full-fetch", None), requests)
                        elif route not in ("bad-mime", "corrupt-full"):
                            self.assertIn("partial check only", result.stdout)
                        self.assertIn(("/" + route, "bytes=0-4194303"), requests)
            finally:
                server.shutdown()
                thread.join()


if __name__ == "__main__":
    unittest.main(verbosity=2)
