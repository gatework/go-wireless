#!/usr/bin/env python3
"""Exercise forbidden and permitted boundaries, including child packages."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("architecture", Path(__file__).with_name("check-architecture.py"))
architecture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(architecture)
MODULE = architecture.MODULE


def package(path, dependency):
    return {"ImportPath": MODULE + ("/" + path if path else ""), "Imports": [dependency]}


class ArchitectureTests(unittest.TestCase):
    def test_domain_children_cannot_bypass_ownership(self):
        cases = [
            ("parser/fields", MODULE + "/vendors/h3c"),
            ("collect/ap", MODULE + "/vendors"),
            ("change/commands", MODULE + "/review/rules"),
            ("vendors/huawei/runtime", MODULE + "/vendors/h3c/fields"),
            ("review/rules", "github.com/gatework/go-net-connector/config"),
            ("collect/ap", "github.com/gatework/go-expect"),
            ("parser/fields", "os/exec"),
            ("change/commands", "net/http/httputil"),
            ("vendors/h3c", MODULE + "/adapter/netconnector"),
            ("", MODULE + "/parser"),
            ("", MODULE + "/internal/records"),
        ]
        for owner, dependency in cases:
            with self.subTest(owner=owner, dependency=dependency):
                self.assertTrue(architecture.ownership_errors([package(owner, dependency)]))

    def test_workflow_children_cannot_import_concrete_dependencies(self):
        for owner in ("internal/workflow/apply", "internal/workflow/apply/steps"):
            for dependency in (
                MODULE + "/internal/bootstrap", MODULE + "/internal/cli/progress",
                MODULE + "/adapter/netconnector", MODULE + "/vendors/h3c", MODULE + "/report",
                "github.com/gatework/go-net-connector/connector", "github.com/gatework/go-expect",
                "github.com/spf13/cobra", "golang.org/x/term",
            ):
                with self.subTest(owner=owner, dependency=dependency):
                    self.assertTrue(architecture.ownership_errors([package(owner, dependency)]))
        self.assertTrue(architecture.ownership_errors([package("internal/bootstrap/config", MODULE + "/internal/cli")]))
        self.assertTrue(architecture.ownership_errors([package("cli/commands", "context")]))

    def test_domain_helper_cannot_hide_infrastructure(self):
        for owner in ("", "collect/ap", "parser/fields"):
            graph = package(owner, MODULE + "/internal/record")
            graph["Deps"] = [MODULE + "/internal/record", "github.com/gatework/go-net-connector/config"]
            with self.subTest(owner=owner):
                self.assertTrue(architecture.ownership_errors([graph]))
        pure = package("parser/fields", "fmt")
        pure["Deps"] = ["fmt", "os"]
        self.assertEqual(architecture.ownership_errors([pure]), [])
        workflow = package("internal/workflow/apply", MODULE + "/internal/nativeerr")
        workflow["Deps"] = [MODULE + "/internal/nativeerr", "github.com/gatework/go-net-connector/connector"]
        self.assertEqual(architecture.ownership_errors([workflow]), [])

    def test_permitted_dependencies_and_path_boundaries(self):
        cases = [
            ("", MODULE + "/internal/record"),
            ("parser/fields", "net/netip"),
            ("review/rules", "net/url"),
            ("vendors/h3c", MODULE + "/parser"),
            ("collect/ap", MODULE + "/parser"),
            ("internal/workflow/apply", MODULE + "/internal/nativeerr"),
            ("internal/workflow/apply", MODULE + "/change"),
            ("internal/workflow/collect", MODULE + "/report"),
            ("internal/workflow/review", MODULE + "/report"),
            ("internal/bootstrap", "github.com/gatework/go-net-connector/config"),
            ("adapter/netconnector", "github.com/gatework/go-net-connector/connector"),
            ("collector", MODULE + "/vendors"),
            ("parser", MODULE + "/vendorship"),
            ("internal/workflow/apply", "github.com/gatework/go-net-connector-tools"),
        ]
        for owner, dependency in cases:
            with self.subTest(owner=owner, dependency=dependency):
                self.assertEqual(architecture.ownership_errors([package(owner, dependency)]), [])


if __name__ == "__main__":
    unittest.main()
