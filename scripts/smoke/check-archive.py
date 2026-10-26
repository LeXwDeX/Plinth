#!/usr/bin/env python3
"""Check an archive prefix without a ZIP central directory (L1 only)."""
import argparse
import pathlib
import re
import struct
import sys
import zipfile
import zlib

MAX_README_BYTES = 4 * 1024 * 1024
# README headings from the Product Schema (model_delivery.archive.readme). The
# section, not the punctuation in a line, decides what a path means: file names
# may legally contain "：" and "（".
INCLUDED_HEADING = "已包含的格式："
MISSING_HEADING = "未包含的格式："
DEPENDENCY_HEADING = "OBJ 的材质与贴图（与 OBJ 放在同一文件夹）："
OTHER_HEADING = "其他文件："
SECTIONS = {
    INCLUDED_HEADING: "model",
    DEPENDENCY_HEADING: "dependency",
    OTHER_HEADING: "other",
    MISSING_HEADING: "missing",
}
# The dependency_missing_line suffix: a file the OBJ references but the task
# never saved, so it is not an archive entry. A saved file whose name happens
# to end with this exact sentence would be misread; upstream names are
# sanitized to ASCII for textures and follow OBJ mtllib text otherwise, so the
# collision is not reachable in practice.
MISSING_REFERENCE_NOTE = "（OBJ 引用了该文件，但任务没有保存它）"
# Only the OBJ format keeps a folder (#222); the label and attachment folder
# come from the Product Schema and are pinned by the offline test.
OBJ_LABEL = "OBJ"
OTHER_DIRECTORY = "other"


def response_metadata(path):
    # curl can include an HTTP proxy CONNECT or interim response. Use the last.
    blocks = re.split(r"\r?\n\r?\n", pathlib.Path(path).read_text(errors="replace"))
    blocks = [b for b in blocks if b.startswith("HTTP/")]
    if not blocks:
        raise ValueError("missing HTTP response headers")
    lines = blocks[-1].splitlines()
    status = int(lines[0].split()[1])
    headers = {}
    for line in lines[1:]:
        if ":" in line:
            key, value = line.split(":", 1)
            headers[key.lower()] = value.strip()
    if status not in (200, 206):
        raise ValueError(f"archive status {status}, expected 200/206")
    if headers.get("content-type", "").split(";")[0].strip().lower() != "application/zip":
        raise ValueError("content-type is not application/zip")
    total = None
    if status == 206:
        match = re.fullmatch(r"bytes 0-(\d+)/(\d+|\*)", headers.get("content-range", ""))
        if not match:
            raise ValueError("invalid Content-Range (must start at byte 0)")
        if match[2] != "*":
            total = int(match[2])
            if int(match[1]) >= total:
                raise ValueError("invalid Content-Range total")
    elif "content-length" in headers:
        total = int(headers["content-length"])
        if total < 0:
            raise ValueError("negative Content-Length")
    return status, total


def check_text(data):
    if re.search(r"https?://", data.decode("utf-8", errors="replace"), re.I):
        raise ValueError("README.txt contains an http(s) address")


def safe_entry(name):
    segments = name.split("/")
    return (
        bool(name)
        and not name.startswith("/")
        and "\\" not in name
        and ":" not in name
        and "\x00" not in name
        and ".." not in segments
        and "" not in segments
    )


def listed_paths(readme):
    """Every path line the README prints, as (path, kind, label).

    kind is "model" (a delivered format), "dependency" (a material or texture
    the ZIP holds), "attachment" (a file under other/) or "reference" (a
    dependency the OBJ names but the task never saved); label is the format
    label for a model line. The section heading decides the kind; punctuation
    inside a file name is kept verbatim.
    """
    section = ""
    paths = []
    for raw in readme.decode("utf-8", errors="replace").splitlines():
        line = raw.strip()
        if not line.startswith("- "):
            section = SECTIONS.get(line, "")
            continue
        entry = line[2:].strip()
        label = ""
        if section == "model":
            # - {label}：{path}; the format label has no separator.
            if "：" not in entry:
                continue
            label, entry = entry.split("：", 1)
            label, entry = label.strip(), entry.strip()
            kind = "model"
        elif section == "dependency":
            kind = "dependency"
            if entry.endswith(MISSING_REFERENCE_NOTE):
                entry = entry[:-len(MISSING_REFERENCE_NOTE)].strip()
                kind = "reference"
        elif section == "other":
            kind = "attachment"
        else:
            continue
        if entry:
            paths.append((entry, kind, label))
    return paths


def check_layout(readme, names=None):
    """Only an OBJ package may use a folder; everything else stays flat (#222)."""
    paths = listed_paths(readme)
    for path, _, _ in paths:
        if not safe_entry(path):
            raise ValueError(f"README lists an unsafe archive path: {path!r}")
    included = [path for path, kind, _ in paths if kind == "model"]
    shipped = [path for path, kind, _ in paths if kind in ("dependency", "attachment")]
    dependencies = [path for path, kind, _ in paths if kind == "dependency"]
    attachments = [path for path, kind, _ in paths if kind == "attachment"]
    obj_paths = [path for path, kind, label in paths if kind == "model" and label == OBJ_LABEL]
    obj_folder = obj_paths[0].rpartition("/")[0] if obj_paths else None
    for path, kind, label in paths:
        if kind == "model" and "/" in path and label != OBJ_LABEL:
            raise ValueError(f"only an OBJ package may keep a folder: {path}")
    # Attachments must live under the Schema's other/ directory; a dependency
    # must sit in the OBJ's folder so its relative references still resolve.
    for path in attachments:
        if not path.startswith(OTHER_DIRECTORY + "/"):
            raise ValueError(f"other file {path!r} is outside {OTHER_DIRECTORY}/")
    if obj_folder is not None:
        if obj_folder == "" and dependencies:
            raise ValueError(f"an OBJ with dependencies must keep its folder: {obj_paths[0]!r}")
        for path in dependencies:
            # Nested references (textures/base.png) are legal, but they must
            # stay inside the OBJ's folder so relative references resolve.
            if not path.startswith(obj_folder + "/"):
                raise ValueError(f"dependency {path!r} is not inside its OBJ folder {obj_folder!r}")
    if names is not None:
        # README and entries must agree in both directions; a reference line
        # names a file that is deliberately absent.
        for path in shipped + included:
            if path not in names:
                raise ValueError(f"README lists {path!r}, which is not in the archive")
    for path in included:
        if "/" not in path:
            continue
        folder = path.split("/", 1)[0] + "/"
        if names is None:
            if not any(other.startswith(folder) and other != path for other in shipped):
                raise ValueError(f"a lone model file was listed in a folder: {path}")
        elif not any(name.startswith(folder) and name != path for name in names):
            raise ValueError(f"a lone model file was packed in a folder: {path}")
    if names is not None:
        mentioned = set(shipped + included)
        for name in names:
            if name == "README.txt" or name.endswith("/"):
                continue
            if name not in mentioned:
                raise ValueError(f"archive entry {name!r} is not described by the README")


def check_prefix(data):
    if len(data) < 30:
        raise ValueError("truncated ZIP local header")
    signature, _, flags, method, _, _, crc, compressed, size, name_len, extra_len = struct.unpack_from(
        "<4s5H3I2H", data
    )
    if signature != b"PK\x03\x04":
        raise ValueError("not a ZIP local file header")
    start = 30 + name_len + extra_len
    if start > len(data):
        raise ValueError("truncated ZIP name/extra fields")
    name = data[30:30 + name_len]
    if name != b"README.txt":
        raise ValueError(f"first entry is {name!r}, expected README.txt")
    if flags & 1 or method not in (0, 8):
        raise ValueError("encrypted or unsupported ZIP compression (expected store/deflate)")
    if compressed == 0xFFFFFFFF or size == 0xFFFFFFFF:
        raise ValueError("ZIP64 README sizes are not supported by this smoke check")
    payload = data[start:]
    descriptor = bool(flags & 8)
    unknown = descriptor and compressed == 0
    if not unknown:
        if compressed > len(payload):
            raise ValueError("README compressed bytes exceed archive prefix")
        payload = payload[:compressed]
    if method == 8:
        inflater = zlib.decompressobj(-15)
        readme = inflater.decompress(payload, MAX_README_BYTES + 1)
        if len(readme) > MAX_README_BYTES or not inflater.eof:
            raise ValueError("README deflate stream incomplete or exceeds 4 MiB")
        consumed = len(payload) - len(inflater.unused_data)
        if not unknown and consumed != compressed:
            raise ValueError("README compressed size mismatch")
    elif unknown:
        # Stored data has no end marker. Accept only a signed, matching descriptor.
        offset = 0
        while True:
            consumed = payload.find(b"PK\x07\x08", offset)
            if consumed < 0 or consumed > MAX_README_BYTES:
                raise ValueError("stored README data descriptor missing from prefix")
            if len(payload) >= consumed + 16:
                got_crc, got_compressed, got_size = struct.unpack_from("<3I", payload, consumed + 4)
                if got_compressed == got_size == consumed and zlib.crc32(payload[:consumed]) == got_crc:
                    break
            offset = consumed + 1
        readme = payload[:consumed]
    else:
        readme, consumed = payload, compressed
    if len(readme) > MAX_README_BYTES:
        raise ValueError("README exceeds 4 MiB")
    if descriptor:
        tail = data[start + consumed:]
        offset = 4 if tail.startswith(b"PK\x07\x08") else 0
        if len(tail) < offset + 12:
            raise ValueError("truncated README data descriptor")
        crc, compressed, size = struct.unpack_from("<3I", tail, offset)
    if len(readme) != size or consumed != compressed or zlib.crc32(readme) != crc:
        raise ValueError("README size/CRC mismatch")
    check_text(readme)
    check_layout(readme)
    return "README.txt first, no provider address, flat single files in README (prefix check)"


def check_full(path):
    with zipfile.ZipFile(path) as archive:
        names = archive.namelist()
        if not names or names[0] != "README.txt":
            raise ValueError(f"first entry is {names[:1]!r}, expected README.txt")
        if archive.infolist()[0].file_size > MAX_README_BYTES:
            raise ValueError("README exceeds 4 MiB")
        readme = archive.read(archive.infolist()[0])
        check_text(readme)
        check_layout(readme, names)
        return f"{len(names)} entries, README.txt first, no provider address, flat single files (full ZIP check)"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("metadata", "partial", "full"))
    parser.add_argument("archive")
    parser.add_argument("headers")
    args = parser.parse_args()
    try:
        status, total = response_metadata(args.headers)
        if args.mode == "metadata":
            print(total if total is not None else "unknown")
        else:
            result = check_full(args.archive) if args.mode == "full" else check_prefix(pathlib.Path(args.archive).read_bytes())
            print(f"{status}, {result}")
    except (ValueError, OSError, zlib.error, zipfile.BadZipFile, EOFError) as error:
        print(str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
