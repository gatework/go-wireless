#!/usr/bin/env python3
"""Check business ownership from Go's actual package graph."""
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent.parent
MODULE = "github.com/gatework/go-wireless"
stream = subprocess.check_output(["go", "list", "-json", "./..."], cwd=ROOT, text=True)
decoder = json.JSONDecoder()
packages = []
while stream.strip():
    value, end = decoder.raw_decode(stream.lstrip())
    packages.append(value)
    stream = stream.lstrip()[end:]

errors = []
for package in packages:
    path = package["ImportPath"]
    local = path.removeprefix(MODULE).lstrip("/")
    imports = package.get("Imports", [])
    for dependency in imports:
        own = dependency.removeprefix(MODULE + "/")
        domain = local == "" or local.split("/")[0] in {"parser", "collect", "review", "change", "vendors"}
        if domain and (dependency.startswith(("github.com/gatework/go-net-connector", "github.com/gatework/go-expect", "github.com/spf13/cobra", "golang.org/x/term")) or dependency in {"os", "net", "net/http", "io/fs", "path/filepath"} or own.startswith(("internal/bootstrap", "internal/cli", "internal/workflow", "adapter/", "report"))):
            errors.append(f"domain {local or 'wireless'} imports infrastructure {dependency}")
        if local == "" and dependency.startswith(MODULE + "/") and not own.startswith("internal/record"):
            errors.append(f"root evidence imports {dependency}")
        if local.startswith("parser") and own.startswith("vendors"):
            errors.append(f"parser imports vendor {dependency}")
        if local == "collect" and own.startswith("vendors"):
            errors.append(f"collection bypasses selected profile through {dependency}")
        if local == "change" and own.startswith("review"):
            errors.append("change depends on review")
        if local.startswith("vendors/huawei") and own.startswith("vendors/h3c"):
            errors.append("Huawei depends on H3C implementation")
        if local.startswith("internal/workflow") and (own.startswith(("internal/cli", "internal/bootstrap")) or dependency.startswith("github.com/spf13/")):
            errors.append(f"workflow imports composition/presentation {dependency}")
        if local.startswith("internal/bootstrap") and own.startswith("internal/cli"):
            errors.append("bootstrap imports CLI")
    if local == "cli":
        errors.append("public CLI package must not exist")

if errors:
    raise SystemExit("\n".join(errors))
print(f"PASS: ownership and import direction for {len(packages)} packages")
