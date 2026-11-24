#!/usr/bin/env python3
"""The local-processing scenarios of smoke-fake-stack.py (model-parts, model-convert).

Offline: no stack, no network. It holds the script's own GLB builder to what the platform's model
input check reads (the container, the exact accessor bounds), the shape helpers to a hand-built
document, and the registration of the two scenarios to the four places that must name them
together (the script's own help/defaults/dispatch, verify-local.py, the CI workflow and
dev_test.md).
"""
import importlib.util
import inspect
import io
import json
import os
import struct
import sys
import unittest
from contextlib import redirect_stdout
from unittest import mock

sys.dont_write_bytecode = True

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ROOT = os.path.dirname(SCRIPTS)
spec = importlib.util.spec_from_file_location("fakestack", os.path.join(SCRIPTS, "smoke", "smoke-fake-stack.py"))
fs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fs)

NEW = ("model-parts", "model-convert")


def read(*parts):
    with open(os.path.join(ROOT, *parts), encoding="utf-8") as handle:
        return handle.read()


class GlbBuilder(unittest.TestCase):
    def build(self, count=3):
        return fs.make_static_glb([(f"part{index}", (0.5, 0.5, 0.5, 1.0)) for index in range(count)])

    def test_the_container_is_a_glb_2_whose_length_is_its_own(self):
        raw = self.build()
        magic, version, total = struct.unpack_from("<4sII", raw)
        self.assertEqual((magic, version, total), (b"glTF", 2, len(raw)))
        json_length, json_kind = struct.unpack_from("<I4s", raw, 12)
        self.assertEqual(json_kind, b"JSON")
        self.assertEqual(json_length % 4, 0)
        bin_length, bin_kind = struct.unpack_from("<I4s", raw, 20 + json_length)
        self.assertEqual(bin_kind, b"BIN\x00")
        self.assertEqual(20 + json_length + 8 + bin_length, len(raw))
        self.assertEqual(bin_length % 4, 0)

    def test_the_buffer_and_accessors_describe_the_bytes(self):
        raw = self.build(3)
        document = fs.glb_document(raw)
        json_length = struct.unpack_from("<I", raw, 12)[0]
        binary = raw[20 + json_length + 8:]
        self.assertEqual(document["buffers"][0]["byteLength"], len(binary))
        for view in document["bufferViews"]:
            self.assertLessEqual(view["byteOffset"] + view["byteLength"], len(binary))
            if view["target"] == 34962:
                self.assertEqual(view["byteOffset"] % 4, 0)
        for accessor in document["accessors"]:
            if accessor["type"] == "VEC3":
                view = document["bufferViews"][accessor["bufferView"]]
                floats = struct.unpack_from("<9f", binary, view["byteOffset"])
                for axis in range(3):
                    column = floats[axis::3]
                    self.assertEqual((min(column), max(column)), (accessor["min"][axis], accessor["max"][axis]))

    def test_the_parts_are_disjoint_nodes_with_their_own_mesh_and_material(self):
        document = fs.glb_document(self.build(3))
        self.assertEqual([node["name"] for node in document["nodes"]], ["part0", "part1", "part2"])
        self.assertEqual(document["scenes"][0]["nodes"], [0, 1, 2])
        self.assertEqual([primitive["material"] for mesh in document["meshes"] for primitive in mesh["primitives"]], [0, 1, 2])
        self.assertEqual(len(document["materials"]), 3)
        boxes = [(a["min"][0], a["max"][0]) for a in document["accessors"] if a["type"] == "VEC3"]
        for (_, high), (low, _) in zip(boxes, boxes[1:]):
            self.assertLess(high, low, "the triangles must not touch")

    def test_the_shape_counts_triangles_node_names_and_meshes(self):
        self.assertEqual(fs.model_shape(fs.glb_document(self.build(3))), (3, ["part0", "part1", "part2"], 3))
        self.assertEqual(fs.model_shape({}), (0, [], 0))

    def test_what_is_not_a_glb_has_no_document(self):
        self.assertIsNone(fs.glb_document(b"this is not a glb"))
        self.assertIsNone(fs.glb_document(b"glTF" + struct.pack("<I", 1) + b"\0" * 20))


class FakeApi:
    """Enough of the Api to run a scenario with no stack: every call is answered by `answers`."""

    def __init__(self):
        self.uploads = []

    def json(self, method, path, body=None, **kwargs):
        return 200, {"id": "session"}

    def upload(self, path, field, filename, data, mime):
        self.uploads.append(filename)
        return 200, {"id": f"asset-{len(self.uploads)}"}


class Scenarios(unittest.TestCase):
    def test_a_stack_without_the_local_offerings_fails_instead_of_skipping(self):
        for scenario in (fs.scenario_model_convert, fs.scenario_model_parts):
            fs.results.clear()
            with redirect_stdout(io.StringIO()):
                scenario(FakeApi(), {})
            self.assertEqual([ok for _, ok in fs.results], [False], scenario.__name__)
        fs.results.clear()

    def test_a_failed_conversion_task_fails_the_scenario(self):
        offerings = {fs.LOCAL_STATIC_GLB_TO_GLTF: {"parameters": [], "outputs": []},
                     fs.LOCAL_STATIC_GLTF_TO_GLB: {"parameters": [], "outputs": []}}
        fs.results.clear()
        with mock.patch.object(fs, "submit", return_value=("task", "")), \
                mock.patch.object(fs, "wait_task", return_value={"state": "FAILED", "failure": {"class": "x"}}), \
                redirect_stdout(io.StringIO()):
            fs.scenario_model_convert(FakeApi(), offerings)
        self.assertIn(False, [ok for _, ok in fs.results])
        fs.results.clear()

    def test_the_scenarios_only_read_offerings_the_script_names(self):
        for name in ("LOCAL_STATIC_GLB_TO_GLTF", "LOCAL_STATIC_GLTF_TO_GLB", "LOCAL_MODEL_SPLIT", "LOCAL_MODEL_MERGE"):
            self.assertTrue(getattr(fs, name).startswith("local/"), name)


class Registration(unittest.TestCase):
    """The scenario registration is written in four places; one forgotten place is a scenario the gate
    or the full run silently does not run."""

    def test_the_script_selects_documents_and_dispatches_both(self):
        source = inspect.getsource(fs.main)
        for name in NEW:
            self.assertGreaterEqual(source.count(f'"{name}"'), 3, f"{name}: default set, stack group and dispatch")
            self.assertIn(name.replace("-", "_"), source.replace("scenario_", ""), name)
        self.assertIn("scenario_model_parts(api, offerings)", source)
        self.assertIn("scenario_model_convert(api, offerings)", source)
        self.assertIn("images,matting-faults,model-parts,model-convert,tripo", source, "the --only help names both")
        for name in NEW:
            self.assertIn(f'"{name}" the', fs.__doc__, f"the usage text describes {name}")

    def test_the_gate_subset_the_ci_job_and_dev_test_run_the_same_scenarios(self):
        verify = read("scripts", "verify-local.py")
        line = next(l for l in verify.splitlines() if l.startswith("FAKE_STACK_SCENARIOS"))
        gate = line.split("'")[1]
        ci = read(".github", "workflows", "ci.yml")
        ci_line = next(l for l in ci.splitlines() if "smoke-fake-stack.py --only" in l)
        ci_only = ci_line.split("--only ")[1].split(" ")[0]
        dev = next(l for l in read("dev_test.md").splitlines() if "smoke-fake-stack.py --only" in l and "Fake 栈冒烟" in l)
        dev_only = dev.split("--only ")[1].split(" ")[0].split("`")[0]
        self.assertEqual(gate, ci_only, "verify-local.py and ci.yml run different scenario sets")
        self.assertEqual(gate, dev_only, "dev_test.md names a different scenario set from verify-local.py")
        for name in NEW:
            self.assertIn(name, gate.split(","), f"{name} is not in the gate subset")


if __name__ == "__main__":
    unittest.main()
