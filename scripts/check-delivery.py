#!/usr/bin/env python3
"""Verify source provenance and a workspace-free, versioned consumer.

The current wireless source is served by a temporary file module proxy.
Its connector dependencies must be published modules; no replace is permitted.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent
MODULE = "github.com/gatework/go-wireless"


def require(condition, message):
    if not condition:
        raise ValueError(message)


def run(args, cwd, env=None):
    print("+", " ".join(map(str, args)), "cwd=" + str(cwd), flush=True)
    subprocess.run(args, cwd=cwd, env=env, check=True, timeout=600)


def output(args, cwd, env):
    return subprocess.check_output(args, cwd=cwd, env=env, text=True, timeout=600)


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify_hashes(root, hashes):
    for path, expected in hashes.items():
        require(sha(root / path) == expected, "checksum mismatch: " + path)


def verify_evidence(root):
    baseline = json.loads((root / "docs/ruby-baseline.json").read_text())
    specs = json.loads((root / "docs/ruby-spec-matrix.json").read_text())
    libs = json.loads((root / "docs/ruby-lib-matrix.json").read_text())
    require(len(specs) == 34 and sum(row["declared_blocks"] for row in specs) == 294,
            "Ruby spec inventory changed")
    for matrix, prefix, suffix in ((specs, "spec/", "_spec.rb"), (libs, "lib/", ".rb")):
        expected = {path for path in baseline["files"] if path.startswith(prefix) and path.endswith(suffix)}
        require({row["source"] for row in matrix} == expected, "Ruby source mapping incomplete")
        for row in matrix:
            for path in row.get("implementation", []):
                require((root / path).is_file(), "missing implementation: " + path)
            for test in row["tests"]:
                path, name = test.split(":", 1)
                require("func " + name + "(" in (root / path).read_text(), "missing test: " + test)
    verify_hashes(root, baseline["corpus"])
    oracle = json.loads((root / "docs/change-oracle.json").read_text())
    verify_hashes(root, oracle["fixture_sha256"])
    print("verified Ruby mappings and unchanged oracle fixtures")


def verify_module_graph(go_mod, stream):
    require(not go_mod.get("Replace"), "go.mod replace directives are forbidden")
    decoder = json.JSONDecoder()
    while stream.strip():
        item, end = decoder.raw_decode(stream.lstrip())
        stream = stream.lstrip()[end:]
        require(not item.get("Replace"), "replaced module: " + item["Path"])
        if not item.get("Main"):
            version = item.get("Version", "")
            require(version and version != "v0.0.0", "unversioned dependency: " + item["Path"])


def check_modules(root, env):
    verify_module_graph(json.loads(output(["go", "mod", "edit", "-json"], root, env)),
                        output(["go", "list", "-m", "-json", "all"], root, env))


def export_source(root, exported):
    allowed_dirs = {"adapter", "parser", "vendors", "collect", "review", "change", "wlan", "rules", "cmd", "internal", "docs", "examples",
                    "report", "scripts", "testdata", ".github"}
    allowed_root = {"go.mod", "go.sum", "LICENSE", "README.md", "GLOSSARY.md", "VERSION", ".gitignore", ".golangci.yml"}
    forbidden = {".env", ".git", ".work", ".superpowers", "wireless-reports", "wireless-executions"}
    selected = []
    for path in sorted(root.rglob("*")):
        rel = path.relative_to(root)
        include = (path.suffix == ".go" or path.name in allowed_root) if len(rel.parts) == 1 else rel.parts[0] in allowed_dirs
        if not include or "__pycache__" in rel.parts or path.suffix == ".pyc":
            continue
        require(not path.is_symlink(), "source symlink: " + str(rel))
        if not path.is_file():
            continue
        require(not forbidden.intersection(rel.parts), "private/generated source path: " + str(rel))
        selected.append(rel)
        destination = exported / rel
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, destination)
    return selected


def publish_local_module(root, selected, proxy):
    digest = hashlib.sha256()
    for rel in selected:
        digest.update(str(rel).encode() + b"\0" + (root / rel).read_bytes() + b"\0")
    version = "v0.1.0-local." + digest.hexdigest()[:20]
    versions = proxy / MODULE / "@v"
    versions.mkdir(parents=True)
    (versions / "list").write_text(version + "\n")
    (versions / (version + ".mod")).write_bytes((root / "go.mod").read_bytes())
    (versions / (version + ".info")).write_text(json.dumps({"Version": version, "Time": "2026-11-07T00:00:00Z"}))
    with zipfile.ZipFile(versions / (version + ".zip"), "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for rel in selected:
            info = zipfile.ZipInfo(MODULE + "@" + version + "/" + rel.as_posix())
            info.external_attr = 0o100644 << 16
            archive.writestr(info, (root / rel).read_bytes())
    return version


def main():
    env = dict(os.environ, GOWORK="off", GOFLAGS="-mod=readonly", CGO_ENABLED="0")
    env.pop("WIRELESS_RUBY_SOURCE", None)
    env.pop("WIRELESS_RUBY", None)
    if len(os.sys.argv) == 2 and os.sys.argv[1] == "--modules-only":
        check_modules(ROOT, env)
        return
    verify_evidence(ROOT)
    with tempfile.TemporaryDirectory(prefix="go-wireless-delivery-") as temporary:
        temporary = Path(temporary).resolve()
        exported = temporary / "export"
        exported.mkdir()
        selected = export_source(ROOT, exported)
        print("clean export:", len(selected), "files; GOWORK=off, no replace")
        check_modules(exported, env)
        run(["go", "mod", "verify"], exported, env)
        run(["go", "mod", "tidy", "-diff"], exported, env)
        run(["go", "build", "-o", str(temporary / "wireless"), "./cmd/wireless"], exported, env)
        run(["go", "run", "./examples/library"], exported, env)
        dependencies = output(["go", "list", "-deps", "."], exported, env)
        require("go-net-connector" not in dependencies and "go-expect" not in dependencies,
                "core package imports a transport dependency")
        proxy = temporary / "proxy"
        version = publish_local_module(exported, selected, proxy)
        consumer = temporary / "consumer"
        consumer.mkdir()
        (consumer / "go.mod").write_text("module example.invalid/consumer\n\ngo 1.26.0\n\nrequire " + MODULE + " " + version + "\n")
        (consumer / "main.go").write_text((exported / "examples/library/main.go").read_text())
        (consumer / "adapter.go").write_text('package main\nimport (\n_ "github.com/gatework/go-wireless/adapter/netconnector"\n_ "github.com/gatework/go-wireless/parser"\n_ "github.com/gatework/go-wireless/collect"\n_ "github.com/gatework/go-wireless/change"\n_ "github.com/gatework/go-wireless/wlan"\n_ "github.com/gatework/go-wireless/rules"\n)\n')
        env["GOPROXY"] = proxy.as_uri() + "," + env.get("GOPROXY", "https://proxy.golang.org,direct")
        env["GONOPROXY"] = "none"
        env["GONOSUMDB"] = ",".join(filter(None, [env.get("GONOSUMDB"), MODULE]))
        env["GOMODCACHE"] = str(temporary / "module-cache")
        run(["go", "mod", "tidy"], consumer, env)
        check_modules(consumer, env)
        run(["go", "mod", "verify"], consumer, env)
        run(["go", "run", "."], consumer, env)
        run([str(temporary / "wireless"), "--help"], consumer, env)
        print("PASS: clean source, published connector dependencies, independent local-proxy consumer")


if __name__ == "__main__":
    main()
