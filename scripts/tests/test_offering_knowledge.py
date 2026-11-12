"""#249: the fake-stack Offering-knowledge scenario's catalog variant.

private_catalog is the only pure helper the scenario needs from the smoke
script: it disables an Offering and shortens a prompt limit for the over-limit
refusal, without touching the shipped Catalog.
"""
import importlib.util
import json
import os
import tempfile
import unittest

SMOKE = os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))),
                     "scripts", "smoke")
spec = importlib.util.spec_from_file_location("fakestack", os.path.join(SMOKE, "smoke-fake-stack.py"))
fakestack = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fakestack)


class PrivateCatalogOfferingKnowledgeTest(unittest.TestCase):
    def test_disables_and_shortens_without_touching_the_source(self):
        with open(os.path.join(fakestack.REPO, "internal", "catalog", "default_catalog.json"),
                  encoding="utf-8") as handle:
            source = json.load(handle)
        source_by_id = {offering["id"]: offering for offering in source["offerings"]}
        with tempfile.TemporaryDirectory() as work:
            path = fakestack.private_catalog(
                work,
                disabled=("tripo/tripo-p2/text-to-3d",),
                prompt_limits={"meshy/meshy-7.1/text-to-3d": 20},
                name="catalog-offering-knowledge.json",
            )
            self.assertEqual(os.path.basename(path), "catalog-offering-knowledge.json")
            with open(path, encoding="utf-8") as handle:
                variant = json.load(handle)
        by_id = {offering["id"]: offering for offering in variant["offerings"]}
        self.assertFalse(by_id["tripo/tripo-p2/text-to-3d"]["enabled"])
        prompt = next(parameter for parameter in by_id["meshy/meshy-7.1/text-to-3d"]["parameters"]
                      if parameter.get("semantic") == "prompt")
        self.assertEqual(prompt["max_length"], 20)
        self.assertTrue(source_by_id["tripo/tripo-p2/text-to-3d"]["enabled"])
        source_prompt = next(parameter for parameter in source_by_id["meshy/meshy-7.1/text-to-3d"]["parameters"]
                             if parameter.get("semantic") == "prompt")
        self.assertEqual(source_prompt["max_length"], 800)

    def test_default_catalog_declares_the_purpose_coverage_contract(self):
        with open(os.path.join(fakestack.REPO, "internal", "catalog", "default_catalog.json"),
                  encoding="utf-8") as handle:
            catalog = json.load(handle)
        usage = catalog["application"]["chat"]["model3d_usage"]
        self.assertTrue(usage["override_coverage_required"])
        game = next(profile for profile in usage["profiles"] if profile["id"] == "game_character")
        override = game["parameter_overrides"]["tripo/tripo-v3.1-multiview/image-to-3d"]
        # #294: a character keeps Tripo on triangle output (a P2 task with quad
        # output delivers an FBX and no GLB); smart low poly accepts at most
        # 20000 triangles.
        self.assertEqual(override["face_limit"], 20000)
        self.assertNotIn("quad", override)
        self.assertTrue(override["smart_low_poly"])


if __name__ == "__main__":
    unittest.main()