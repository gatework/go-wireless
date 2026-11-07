#!/usr/bin/env python3
"""Verify frozen allowlisted dependencies and create a relative local workspace."""
import hashlib
import json
import os
import re
from pathlib import Path, PurePosixPath
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parent.parent
DEPS = ROOT / 'dependencies'

def digest(data):
    return hashlib.sha256(data).hexdigest()

def safe_name(name):
    p = PurePosixPath(name)
    if p.is_absolute() or not p.parts or any(x in ('', '.', '..') for x in name.split('/')) or '\\' in name:
        raise ValueError('unsafe dependency path')
    return p

def validate_patch(data, original, patched):
    """Accept only canonical unified text diffs with allowlisted module targets."""
    text = data.decode('utf-8')
    if '\r' in text or '\x00' in text:
        raise ValueError('unsupported patch encoding')
    lines = text.splitlines(keepends=True)
    index, seen = 0, set()
    def target(line, marker, prefix, allowed):
        if not line.startswith(marker) or not line.endswith('\n'):
            raise ValueError('unsupported patch header')
        value = line[len(marker):-1]
        if value == '/dev/null':
            return None
        if not value.startswith(prefix) or not re.fullmatch(r'[A-Za-z0-9_./-]+', value):
            raise ValueError('ambiguous patch target')
        rel = str(safe_name(value[len(prefix):]))
        if rel not in allowed:
            raise ValueError('unlisted patch target')
        return rel
    while index < len(lines):
        if index + 2 >= len(lines):
            raise ValueError('incomplete patch')
        before = target(lines[index], '--- ', 'a/', original)
        after = target(lines[index+1], '+++ ', 'b/', patched)
        path = before or after
        if path is None or before and after and before != after or path in seen:
            raise ValueError('unsupported rename or duplicate patch target')
        seen.add(path)
        index += 2
        hunks = 0
        while index < len(lines) and lines[index].startswith('@@ '):
            header = re.fullmatch(r'@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@[^\n]*\n', lines[index])
            if header is None:
                raise ValueError('invalid unified hunk')
            old_count = int(header[2]) if header[2] is not None else 1
            new_count = int(header[4]) if header[4] is not None else 1
            if old_count + new_count == 0:
                raise ValueError('empty patch hunk')
            index += 1
            while old_count or new_count:
                if index >= len(lines) or not lines[index].endswith('\n'):
                    raise ValueError('incomplete patch hunk')
                prefix = lines[index][:1]
                if prefix not in (' ', '+', '-'):
                    raise ValueError('unsupported patch form')
                old_count -= prefix in (' ', '-')
                new_count -= prefix in (' ', '+')
                if old_count < 0 or new_count < 0:
                    raise ValueError('invalid patch hunk counts')
                index += 1
            hunks += 1
        if not hunks:
            raise ValueError('patch requires unified hunks')
    if not seen:
        raise ValueError('empty patch')

def preflight_destinations(names):
    for name in names:
        if str(safe_name(name)) != name or '/' in name:
            raise ValueError('invalid module name')
        path = ROOT/'.work'/name
        if path.is_symlink() or path.exists() and not path.is_dir():
            raise ValueError('module destination must be a directory, not a symlink')
    path = ROOT/'go.work'
    if path.is_symlink() or path.exists() and not path.is_file():
        raise ValueError('go.work must be a regular file, not a symlink')

def publish(temp, names):
    """Retain old destinations until every staged rename succeeds; roll back on error."""
    preflight_destinations(names)
    backup = Path(tempfile.mkdtemp(prefix='previous-', dir=ROOT/'.work'))
    journal = []
    try:
        pairs = [(temp/name, ROOT/'.work'/name) for name in names]
        pairs.append((temp/'go.work', ROOT/'go.work'))
        for i, (source, target) in enumerate(pairs):
            old = backup/str(i)
            state = {'target':target, 'old':old, 'saved':False, 'published':False}
            journal.append(state)
            if target.exists():
                os.replace(target, old)
                state['saved'] = True
            os.replace(source, target)
            state['published'] = True
    except BaseException:
        rollback_errors = []
        for state in reversed(journal):
            try:
                target = state['target']
                if state['published']:
                    if target.is_dir():
                        shutil.rmtree(target)
                    else:
                        target.unlink()
                if state['saved']:
                    os.replace(state['old'], target)
            except BaseException as error:
                rollback_errors.append(error)
        if rollback_errors:
            # Never remove retained originals when recovery itself failed.
            raise RuntimeError('workspace rollback incomplete; originals retained at '+str(backup)) from rollback_errors[0]
        shutil.rmtree(backup)
        raise
    else:
        shutil.rmtree(backup)

def prepare():
    if DEPS.is_symlink() or (DEPS / 'manifest.json').is_symlink():
        raise ValueError('dependency metadata must not be a symlink')
    manifest = json.loads((DEPS / 'manifest.json').read_text())
    work = ROOT / '.work'
    if work.is_symlink():
        raise ValueError('workspace must not be a symlink')
    work.mkdir(exist_ok=True)
    preflight_destinations(manifest['dependencies'])
    with tempfile.TemporaryDirectory(prefix='prepare-', dir=work) as temp:
        temp = Path(temp)
        for name, spec in manifest['dependencies'].items():
            if str(safe_name(name)) != name or '/' in name:
                raise ValueError('invalid module name')
            archive_name = str(safe_name(spec['archive']))
            if '/' in archive_name:
                raise ValueError('archive must be a direct dependency file')
            archive = DEPS / archive_name
            if archive.is_symlink() or digest(archive.read_bytes()) != spec['sha256']:
                raise ValueError('dependency archive checksum mismatch')
            expected = spec['files']
            for path in set(expected) | set(spec['patched_files']):
                safe_name(path)
            seen = set()
            with tarfile.open(archive, 'r:gz') as tar:
                for member in tar:
                    p = safe_name(member.name)
                    if not member.isfile() or p.parts[0] != name or len(p.parts) < 2:
                        raise ValueError('unexpected archive member')
                    rel = '/'.join(p.parts[1:])
                    if rel not in expected or rel in seen:
                        raise ValueError('unlisted or duplicate archive member')
                    data = tar.extractfile(member).read()
                    if digest(data) != expected[rel]:
                        raise ValueError('dependency file checksum mismatch')
                    out = temp / p
                    out.parent.mkdir(parents=True, exist_ok=True)
                    out.write_bytes(data)
                    seen.add(rel)
            if seen != set(expected):
                raise ValueError('missing dependency member')
            for patch in spec['patches']:
                patch_name = str(safe_name(patch['file']))
                if '/' in patch_name:
                    raise ValueError('patch must be a direct dependency file')
                path = DEPS / patch_name
                if path.is_symlink() or digest(path.read_bytes()) != patch['sha256']:
                    raise ValueError('patch checksum mismatch')
                validate_patch(path.read_bytes(), spec['files'], spec['patched_files'])
                subprocess.run(['patch', '-p1', '--batch', '-F0', '-i', str(path)], cwd=temp/name, check=True, stdout=subprocess.DEVNULL)
            if any(p.is_symlink() for p in (temp/name).rglob('*')):
                raise ValueError('patched source must not contain symlinks')
            actual = {p.relative_to(temp/name).as_posix(): digest(p.read_bytes()) for p in (temp/name).rglob('*') if p.is_file()}
            if actual != spec['patched_files']:
                raise ValueError('patched dependency checksum mismatch')
        (temp/'go.work').write_text('go 1.26.0\n\nuse (\n\t.\n' + ''.join('\t./.work/'+name+'\n' for name in sorted(manifest['dependencies'])) + ')\n\n' + ''.join('replace github.com/gatework/'+name+' v0.0.0 => ./.work/'+name+'\n' for name in sorted(manifest['dependencies'])))
        publish(temp, list(manifest['dependencies']))
    print('Verified frozen sources and patches; generated relative go.work')

if __name__ == '__main__':
    prepare()
