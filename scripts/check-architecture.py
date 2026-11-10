#!/usr/bin/env python3
"""Check business ownership from Go's actual package graph."""
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent.parent
MODULE = "github.com/gatework/go-wireless"
NATIVE_MODULES = ("github.com/gatework/go-net-connector", "github.com/gatework/go-expect")


def within(path, parent):
    return path == parent or path.startswith(parent + "/")


def under_any(path, parents):
    return any(within(path, parent) for parent in parents)


def ownership_errors(packages):
    errors = []
    for package in packages:
        path = package["ImportPath"]
        if not within(path, MODULE):
            continue
        local = path.removeprefix(MODULE).lstrip("/")
        domain = not local or under_any(local, ("parser", "collect", "review", "change", "vendors", "wlan", "rules"))
        direct = set(package.get("Imports", []))
        dependencies = sorted(direct | set(package.get("Deps", [])))
        for dependency in dependencies:
            own = dependency.removeprefix(MODULE + "/") if dependency.startswith(MODULE + "/") else ""
            native = under_any(dependency, NATIVE_MODULES)
            presentation = under_any(dependency, ("github.com/spf13/cobra", "golang.org/x/term"))
            file_network = dependency in {"net", "io/fs", "path/filepath"} or under_any(dependency, ("os", "net/http"))
            composition = under_any(own, ("internal/bootstrap", "internal/cli", "internal/workflow", "adapter", "report"))
            if domain and (native or presentation or composition or dependency in direct and file_network):
                errors.append(f"domain {local or 'wireless'} imports infrastructure {dependency}")
            if not local and own and not within(own, "internal/record"):
                errors.append(f"root evidence imports {dependency}")
            if within(local, "parser") and within(own, "vendors"):
                errors.append(f"parser imports vendor {dependency}")
            if within(local, "collect") and within(own, "vendors"):
                errors.append(f"collection bypasses selected profile through {dependency}")
            if within(local, "change") and within(own, "review"):
                errors.append(f"change depends on review {dependency}")
            # The typed model depends only on go-netops; the rule catalog only
            # on the model and the typed change it proposes.
            if within(local, "wlan") and own:
                errors.append(f"typed model wlan imports {dependency}")
            if within(local, "rules") and dependency in direct and own and not under_any(own, ("wlan", "change")):
                errors.append(f"rule catalog imports {dependency}")
            if within(local, "vendors/huawei") and within(own, "vendors/h3c"):
                errors.append(f"Huawei depends on H3C implementation {dependency}")
            if within(local, "internal/workflow") and dependency in direct:
                forbidden = under_any(own, ("internal/cli", "internal/bootstrap", "adapter", "vendors"))
                if forbidden or native or under_any(dependency, ("github.com/spf13", "golang.org/x/term")):
                    errors.append(f"workflow imports composition/transport/presentation {dependency}")
                if within(local, "internal/workflow/apply") and within(own, "report"):
                    errors.append(f"apply workflow depends on concrete publication {dependency}")
            if within(local, "internal/bootstrap") and dependency in direct and within(own, "internal/cli"):
                errors.append(f"bootstrap imports CLI {dependency}")
        if within(local, "cli"):
            errors.append("public CLI package must not exist")
    return errors


def package_graph():
    stream = subprocess.check_output(["go", "list", "-json", "./..."], cwd=ROOT, text=True)
    decoder = json.JSONDecoder()
    packages = []
    while stream.strip():
        value, end = decoder.raw_decode(stream.lstrip())
        packages.append(value)
        stream = stream.lstrip()[end:]
    return packages


def main():
    packages = package_graph()
    errors = ownership_errors(packages)
    if errors:
        raise SystemExit("\n".join(errors))
    print(f"PASS: ownership and import direction for {len(packages)} packages")


if __name__ == "__main__":
    main()
