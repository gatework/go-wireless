#!/usr/bin/env python3
"""Negative controls for safe dependency reconstruction; entirely temporary."""
import gzip
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock
import copy

spec = importlib.util.spec_from_file_location('prepare', Path(__file__).with_name('prepare-workspace.py'))
prepare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prepare)

class BootstrapTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        prepare.ROOT = Path(self.tmp.name)
        prepare.DEPS = prepare.ROOT / 'dependencies'
        prepare.DEPS.mkdir()
    def archive(self, members=None, expected=None):
        if members is None:
            members = [('go-expect/go.mod', b'module example\n', 'file')]
        out = io.BytesIO()
        with tarfile.open(fileobj=out, mode='w') as tar:
            for name, data, kind in members:
                entry = tarfile.TarInfo(name)
                entry.size = len(data)
                if kind == 'symlink':
                    entry.type = tarfile.SYMTYPE
                    entry.linkname = '/tmp/escape'
                    entry.size = 0
                tar.addfile(entry, io.BytesIO(data) if kind == 'file' else None)
        data = gzip.compress(out.getvalue(), mtime=0)
        (prepare.DEPS/'source.tar.gz').write_bytes(data)
        if expected is None:
            expected = {'go.mod': hashlib.sha256(b'module example\n').hexdigest()}
        manifest = {'dependencies': {'go-expect': {'archive':'source.tar.gz', 'sha256':hashlib.sha256(data).hexdigest(), 'files':expected, 'patched_files':expected, 'patches':[]}}}
        (prepare.DEPS/'manifest.json').write_text(json.dumps(manifest))
    def test_deterministic_reconstruction(self):
        self.archive()
        prepare.prepare()
        work = (prepare.ROOT/'go.work').read_bytes()
        prepare.prepare()
        self.assertEqual(work, (prepare.ROOT/'go.work').read_bytes())
        self.assertIn(b'./.work/go-expect', work)
        self.assertNotIn(str(prepare.ROOT).encode(), work)
    def test_reject_member_paths_and_links(self):
        for name, kind in [('../escape','file'),('/absolute','file'),('go-expect/../escape','file'),('go-expect/link','symlink'),('other/go.mod','file'),('go-expect/./go.mod','file'),('go-expect\\escape','file')]:
            with self.subTest(name=name):
                self.archive([(name,b'x',kind)])
                with self.assertRaises(ValueError): prepare.prepare()
    def test_reject_duplicate_and_missing(self):
        member = ('go-expect/go.mod',b'module example\n','file')
        for members in [[member,member],[]]:
            self.archive(members)
            with self.assertRaises(ValueError): prepare.prepare()
    def test_reject_content_hash(self):
        self.archive([('go-expect/go.mod',b'changed','file')])
        with self.assertRaises(ValueError): prepare.prepare()
    def test_reject_archive_hash(self):
        self.archive()
        with (prepare.DEPS/'source.tar.gz').open('ab') as f: f.write(b'changed')
        with self.assertRaises(ValueError): prepare.prepare()

    def test_corrupt_inputs_preserve_previously_valid_workspace(self):
        self.archive()
        prepare.prepare()
        before = {p.relative_to(prepare.ROOT): p.read_bytes()
                  for p in (prepare.ROOT/'.work').rglob('*') if p.is_file()}
        before[Path('go.work')] = (prepare.ROOT/'go.work').read_bytes()
        manifest = (prepare.DEPS/'manifest.json').read_bytes()
        archive = (prepare.DEPS/'source.tar.gz').read_bytes()
        for kind in ['manifest', 'archive', 'traversal']:
            with self.subTest(kind=kind):
                (prepare.DEPS/'manifest.json').write_bytes(manifest)
                (prepare.DEPS/'source.tar.gz').write_bytes(archive)
                if kind == 'manifest':
                    (prepare.DEPS/'manifest.json').write_bytes(b'{broken')
                elif kind == 'archive':
                    (prepare.DEPS/'source.tar.gz').write_bytes(archive+b'corrupt')
                else:
                    self.archive([('go-expect/../../escape', b'no', 'file')])
                with self.assertRaises(ValueError): prepare.prepare()
                for path, content in before.items():
                    self.assertEqual((prepare.ROOT/path).read_bytes(), content)
                self.assertFalse((prepare.ROOT/'escape').exists())
    def test_reject_workspace_symlink(self):
        self.archive()
        (prepare.ROOT/'.work').symlink_to(prepare.DEPS, target_is_directory=True)
        with self.assertRaises(ValueError): prepare.prepare()
    def test_reject_destination_symlink(self):
        self.archive()
        (prepare.ROOT/'.work').mkdir()
        (prepare.ROOT/'.work/go-expect').symlink_to(prepare.DEPS, target_is_directory=True)
        with self.assertRaises(ValueError): prepare.prepare()

    def add_patch(self, text):
        data = text.encode()
        (prepare.DEPS/'test.patch').write_bytes(data)
        path = prepare.DEPS/'manifest.json'
        m = json.loads(path.read_text())
        m['dependencies']['go-expect']['patches'] = [{'file':'test.patch', 'sha256':hashlib.sha256(data).hexdigest()}]
        path.write_text(json.dumps(m))
    def two_modules(self):
        self.archive()
        path = prepare.DEPS/'manifest.json'
        m = json.loads(path.read_text())
        data = io.BytesIO()
        with tarfile.open(fileobj=data, mode='w') as tar:
            content = b'module example\n'
            ent = tarfile.TarInfo('second/go.mod')
            ent.size = len(content)
            tar.addfile(ent, io.BytesIO(content))
        raw = gzip.compress(data.getvalue(),mtime=0)
        (prepare.DEPS/'second.tar.gz').write_bytes(raw)
        m['dependencies']['second'] = copy.deepcopy(m['dependencies']['go-expect'])
        m['dependencies']['second'].update(archive='second.tar.gz',sha256=hashlib.sha256(raw).hexdigest())
        path.write_text(json.dumps(m))
        work = prepare.ROOT/'.work'
        work.mkdir()
        for name in m['dependencies']:
            (work/name).mkdir()
            (work/name/'prior').write_text(name)
        (prepare.ROOT/'go.work').write_text('prior workspace')
    def test_review_patch_traversal_does_not_touch_sentinel(self):
        self.archive()
        sentinel = prepare.ROOT/'victim'
        sentinel.write_text('before\n')
        self.add_patch('--- a/../../../victim\n+++ b/../../../victim\n@@ -1 +1 @@\n-before\n+after\n')
        try:
            with self.assertRaises(ValueError): prepare.prepare()
        finally:
            self.assertEqual(sentinel.read_text(), 'before\n')
    def test_review_go_work_symlink_is_not_followed(self):
        self.archive()
        sentinel = prepare.ROOT/'victim'
        sentinel.write_text('before')
        (prepare.ROOT/'go.work').symlink_to(sentinel)
        try:
            with self.assertRaises(ValueError): prepare.prepare()
        finally:
            self.assertEqual(sentinel.read_text(), 'before')
    def test_review_later_destination_preflight_preserves_first(self):
        self.two_modules()
        second = prepare.ROOT/'.work/second'
        (second/'prior').unlink()
        second.rmdir()
        second.symlink_to(prepare.DEPS, target_is_directory=True)
        with self.assertRaises(ValueError): prepare.prepare()
        self.assertEqual((prepare.ROOT/'.work/go-expect/prior').read_text(), 'go-expect')
        self.assertEqual((prepare.ROOT/'go.work').read_text(), 'prior workspace')
    def test_review_publication_failure_rolls_back_all_destinations(self):
        self.two_modules()
        real_replace = prepare.os.replace
        def fail_once(src, dst):
            if Path(src).name == 'second' and Path(src).parent.name.startswith('prepare-') and Path(dst) == prepare.ROOT/'.work/second':
                raise OSError('injected publication failure')
            return real_replace(src, dst)
        with mock.patch.object(prepare.os, 'replace', side_effect=fail_once):
            with self.assertRaises(OSError): prepare.prepare()
        for name in ['go-expect','second']:
            self.assertEqual((prepare.ROOT/'.work'/name/'prior').read_text(), name)
        self.assertEqual((prepare.ROOT/'go.work').read_text(), 'prior workspace')

    def test_review_reject_ambiguous_and_unsupported_patch_forms(self):
        self.archive()
        for text in [
            '--- /tmp/target\n+++ /tmp/target\n@@ -1 +1 @@\n-x\n+y\n',
            '--- a/go.mod\t2026-01-01\n+++ b/go.mod\n@@ -1 +1 @@\n-x\n+y\n',
            'diff --git a/go.mod b/go.mod\nBinary files differ\n',
            '*** a/go.mod\n--- b/go.mod\n',
            '--- a/go.mod\n+++ b/go.mod\n@@ -1 +1 @@\n-x\n+y\nmalformed trailing input\n',
            '--- a/go.mod\n+++ b/other\n@@ -1 +1 @@\n-x\n+y\n',
        ]:
            with self.subTest(text=text):
                self.add_patch(text)
                with self.assertRaises(ValueError): prepare.prepare()
    def test_review_go_work_publication_failure_restores_all_modules(self):
        self.two_modules()
        real_replace = prepare.os.replace
        def fail_once(src, dst):
            if Path(src).name == 'go.work' and Path(src).parent.name.startswith('prepare-'):
                raise OSError('injected workspace publication failure')
            return real_replace(src,dst)
        with mock.patch.object(prepare.os,'replace',side_effect=fail_once):
            with self.assertRaises(OSError): prepare.prepare()
        for name in ['go-expect','second']:
            self.assertEqual((prepare.ROOT/'.work'/name/'prior').read_text(),name)
        self.assertEqual((prepare.ROOT/'go.work').read_text(),'prior workspace')

    def test_review_rollback_failure_retains_original_backup(self):
        self.two_modules()
        real_replace = prepare.os.replace
        def fail_publication_and_one_restore(src, dst):
            src, dst = Path(src), Path(dst)
            if src.name == 'second' and src.parent.name.startswith('prepare-'):
                raise OSError('injected publication failure')
            if src.name == '0' and src.parent.name.startswith('previous-'):
                raise OSError('injected rollback failure')
            return real_replace(src,dst)
        with mock.patch.object(prepare.os,'replace',side_effect=fail_publication_and_one_restore):
            with self.assertRaisesRegex(RuntimeError,'originals retained'):
                prepare.prepare()
        backups = list((prepare.ROOT/'.work').glob('previous-*'))
        self.assertEqual(len(backups),1)
        self.assertEqual((backups[0]/'0/prior').read_text(),'go-expect')
        self.assertEqual((prepare.ROOT/'.work/second/prior').read_text(),'second')
        self.assertEqual((prepare.ROOT/'go.work').read_text(),'prior workspace')

if __name__ == '__main__':
    unittest.main()
