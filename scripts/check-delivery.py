#!/usr/bin/env python3
"""Local delivery acceptance: audited source export + independent consumer.

Requires Python 3, patch and Go >=1.26. Ordinary third-party modules may download.
No Ruby, sibling checkout, device, external inventory or running service is used.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parent.parent

def run(args, cwd, env=None):
    print('+', ' '.join(map(str, args)), 'cwd='+str(cwd), flush=True)
    subprocess.run(args, cwd=cwd, env=env, check=True)

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def main():
    # Source snapshots are immutable artifacts; rebuild validates source AND patches.
    manifest = json.loads((ROOT/'dependencies/manifest.json').read_text())
    for line in (ROOT/'dependencies/SHA256SUMS').read_text().splitlines():
        expected, filename = line.split(None, 1)
        assert sha(ROOT/'dependencies'/filename.strip()) == expected, filename
    for name, spec in manifest['dependencies'].items():
        with tarfile.open(ROOT/'dependencies'/spec['archive']) as archive:
            names = archive.getnames()
            assert sorted(names) == sorted(name+'/'+p for p in spec['files'])
            for member in archive:
                assert member.isfile(), member.name
                parts = Path(member.name).parts
                assert '..' not in parts and not member.name.startswith('/')
                assert all(p not in {'.env','.git','captures','backups','reports','.superpowers'} for p in parts)
                assert not member.name.endswith(('_test.go','.bak','.log','.bin'))
            assert name+'/LICENSE' in names
        print('verified archive allowlist:', name, len(names), 'regular build files + license')
    # Every spec matrix row names concrete tests present in this independent tree.
    matrix = json.loads((ROOT/'docs/ruby-spec-matrix.json').read_text())
    assert len(matrix) == 34 and sum(x['declared_blocks'] for x in matrix) == 294
    baseline = json.loads((ROOT/'docs/ruby-baseline.json').read_text())
    assert {x['source'] for x in matrix} == {x for x in baseline['files'] if x.startswith('spec/') and x.endswith('_spec.rb')}
    lib_matrix = json.loads((ROOT/'docs/ruby-lib-matrix.json').read_text())
    assert {x['source'] for x in lib_matrix} == {x for x in baseline['files'] if x.startswith('lib/') and x.endswith('.rb')}
    for row in lib_matrix:
        for path in row['implementation']: assert (ROOT/path).is_file(), path
        for test in row['tests']:
            path, name = test.split(':')
            assert 'func '+name+'(' in (ROOT/path).read_text(), test
    for row in matrix:
        for test in row['tests']:
            path, name = test.split(':')
            assert 'func '+name+'(' in (ROOT/path).read_text(), test
    for source in json.loads((ROOT/'docs/ruby-baseline.json').read_text())['corpus']:
        assert sha(ROOT/source) == json.loads((ROOT/'docs/ruby-baseline.json').read_text())['corpus'][source]
    oracle = json.loads((ROOT/'docs/change-oracle.json').read_text())
    for path, expected in oracle['fixture_sha256'].items():
        assert sha(ROOT/path) == expected, path
    print('verified 34 spec mappings, 294 declared blocks, frozen oracle hashes')
    with tempfile.TemporaryDirectory(prefix='go-wireless-delivery-') as temporary:
        temporary = Path(temporary).resolve()
        exported = temporary/'export'
        exported.mkdir()
        allowed_dirs = {'adapter','cli','cmd','dependencies','docs','examples','report','scripts','testdata'}
        allowed_root = {'go.mod','go.sum','LICENSE','README.md','.gitignore'}
        selected = []
        for path in sorted(ROOT.rglob('*')):
            rel = path.relative_to(ROOT)
            if path.is_symlink():
                if rel.parts[0] in allowed_dirs: raise ValueError('source symlink: '+str(rel))
                continue
            if not path.is_file(): continue
            if len(rel.parts) == 1:
                include = path.suffix == '.go' or path.name in allowed_root
            else:
                include = rel.parts[0] in allowed_dirs and '__pycache__' not in rel.parts and path.suffix != '.pyc'
            if not include: continue
            assert all(p not in {'.env','.git','.work','.superpowers','wireless-reports','wireless-executions'} for p in rel.parts)
            selected.append(rel)
            dest = exported/rel
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path,dest)
        print('clean export:', len(selected), 'files; no workspace, sibling path, Git data or generated reports')
        run(['python3','scripts/prepare-workspace.py'],exported)
        first = {p.relative_to(exported):sha(p) for p in (exported/'.work').rglob('*') if p.is_file()}
        run(['python3','scripts/prepare-workspace.py'],exported)
        second = {p.relative_to(exported):sha(p) for p in (exported/'.work').rglob('*') if p.is_file()}
        assert first == second
        assert '/Users/' not in (exported/'go.work').read_text()
        env = dict(os.environ)
        env.pop('WIRELESS_RUBY_SOURCE',None)
        env.pop('WIRELESS_RUBY',None)
        env['GOWORK'] = str(exported/'go.work')
        env['CGO_ENABLED'] = '0'
        modules = subprocess.check_output(['go','list','-m','-json','all'],cwd=exported,env=env,text=True)
        decoder = json.JSONDecoder()
        while modules.strip():
            item, end = decoder.raw_decode(modules.lstrip())
            modules = modules.lstrip()[end:]
            if item['Path'].startswith('github.com/gatework/'):
                location = Path(item.get('Replace',item).get('Dir',item.get('Dir',''))).resolve()
                assert location == exported or exported in location.parents, item
                print('verified local module:', item['Path'], location.relative_to(exported))
        run(['go','build','-o',str(temporary/'wireless'),'./cmd/wireless'],exported,env)
        run(['go','run','./examples/library'],exported,env)
        dependencies = subprocess.check_output(['go','list','-deps','.'],cwd=exported,env=env,text=True)
        assert 'go-net-connector' not in dependencies and 'go-expect' not in dependencies
        consumer = temporary/'consumer'
        consumer.mkdir()
        (consumer/'go.mod').write_text('module example.invalid/consumer\n\ngo 1.26.0\n\nrequire github.com/gatework/go-wireless v0.0.0\n')
        (consumer/'main.go').write_text((exported/'examples/library/main.go').read_text())
        (consumer/'go.work').write_text('go 1.26.0\n\nuse (\n .\n ../export\n ../export/.work/go-expect\n ../export/.work/go-net-connector\n)\n\nreplace github.com/gatework/go-wireless v0.0.0 => ../export\nreplace github.com/gatework/go-net-connector v0.0.0 => ../export/.work/go-net-connector\nreplace github.com/gatework/go-expect v0.0.0 => ../export/.work/go-expect\n')
        env['GOWORK'] = str(consumer/'go.work')
        run(['go','run','.'],consumer,env)
        run([str(temporary/'wireless'),'--help'],consumer,env)
        print('PASS: deterministic frozen reconstruction, standalone CLI and independent library consumer')

if __name__ == '__main__': main()
