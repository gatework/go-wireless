#!/usr/bin/env python3
"""Negative controls must also fail when Python assertions are disabled."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("delivery", Path(__file__).with_name("check-delivery.py"))
delivery = importlib.util.module_from_spec(spec)
spec.loader.exec_module(delivery)


class DeliveryTests(unittest.TestCase):
    def test_corrupted_evidence_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "fixture").write_text("original")
            hashes = {"fixture": delivery.sha(root / "fixture")}
            delivery.verify_hashes(root, hashes)
            (root / "fixture").write_text("corrupted")
            with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                delivery.verify_hashes(root, hashes)

    def test_replacement_and_placeholder_are_rejected(self):
        module = {"Path": "example.invalid/dependency", "Version": "v1.2.3"}
        delivery.verify_module_graph({}, json.dumps(module))
        for manifest, graph in (({"Replace": [{}]}, module),
                                ({}, dict(module, Replace={"Dir": "../sibling"})),
                                ({}, dict(module, Version="v0.0.0")),
                                ({}, dict(module, Version=""))):
            with self.subTest(manifest=manifest, graph=graph), self.assertRaises(ValueError):
                delivery.verify_module_graph(manifest, json.dumps(graph))

    def test_export_rejects_secret_and_symlink(self):
        for symlink in (False, True):
            with self.subTest(symlink=symlink), tempfile.TemporaryDirectory() as directory:
                root = Path(directory) / "source"
                exported = Path(directory) / "export"
                (root / "cli").mkdir(parents=True)
                if symlink:
                    (root / "cli/escape.go").symlink_to("/dev/null")
                else:
                    (root / "cli/.env").write_text("secret")
                with self.assertRaises(ValueError):
                    delivery.export_source(root, exported)


if __name__ == "__main__":
    unittest.main()
