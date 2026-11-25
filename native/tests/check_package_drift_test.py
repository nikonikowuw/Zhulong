#!/usr/bin/env python3
"""Unit tests for native/scripts/check_package_drift.py."""

import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "native/scripts/check_package_drift.py"
SPEC = importlib.util.spec_from_file_location("check_package_drift", SCRIPT)
drift = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(drift)


def _write_target(
    pkg_dir: Path,
    target: str,
    *,
    manifest: dict | str | None = None,
    schema: bytes | None = b'{"type":"object"}\n',
    src_files: dict[str, bytes] | None = None,
) -> Path:
    tdir = pkg_dir / target
    tdir.mkdir(parents=True, exist_ok=True)
    if manifest is None:
        manifest = {
            "id": pkg_dir.name,
            "name": "Demo Algo",
            "version": "1.0.0",
            "task_type": "detection",
            "category": "safety",
            "platform": {"arch": "x86_64" if target == "onnx" else "aarch64", "target": target},
        }
    if isinstance(manifest, str):
        (tdir / "manifest.json").write_text(manifest, encoding="utf-8")
    else:
        (tdir / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
    if schema is not None:
        (tdir / "config.schema.json").write_bytes(schema)
    for rel, content in (src_files or {"src/algo.cpp": b"int f() { return 1; }\n"}).items():
        path = tdir / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
    return tdir


class PackageDriftTests(unittest.TestCase):
    def _run_check(self, pkg_dir: Path, allowed_diff=None, allowed_one_sided=None):
        stdout = io.StringIO()
        stderr = io.StringIO()
        with patch.object(drift, "ALLOWED_DIFFERENCES", allowed_diff or {}), \
             patch.object(drift, "ALLOWED_ONE_SIDED", allowed_one_sided or {}), \
             contextlib.redirect_stdout(stdout), \
             contextlib.redirect_stderr(stderr):
            rc = drift.check_package(pkg_dir)
        return rc, stdout.getvalue(), stderr.getvalue()

    def test_malformed_manifest_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            pkg = Path(tmp) / "demo_pkg"
            _write_target(pkg, "onnx")
            _write_target(pkg, "rk3568", manifest="{not valid json")
            rc, _, err = self._run_check(pkg)
            self.assertEqual(rc, 1)
            self.assertIn("无法解析 manifest", err)

            # CLI exit code must also be non-zero
            proc = subprocess.run([sys.executable, str(SCRIPT), str(pkg)], capture_output=True, text=True, timeout=15)
            self.assertNotEqual(proc.returncode, 0)

    def test_manifest_missing_platform_target_or_non_object_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            pkg = Path(tmp) / "demo_pkg"
            _write_target(pkg, "onnx", manifest="[]")
            rc, _, err = self._run_check(pkg)
            self.assertEqual(rc, 1)
            self.assertIn("JSON 对象", err)

            _write_target(pkg, "onnx", manifest={"id": "demo_pkg", "platform": {}})
            rc, _, err = self._run_check(pkg)
            self.assertEqual(rc, 1)
            self.assertIn("platform.target", err)

    def test_config_schema_byte_equality_and_one_sided_detection(self):
        with tempfile.TemporaryDirectory() as tmp:
            pkg = Path(tmp) / "demo_pkg"
            _write_target(pkg, "onnx", schema=b'{"type":"object"}\n')
            _write_target(pkg, "rk3568", schema=b'{"type":"object"}\n')
            rc, _, _ = self._run_check(pkg)
            self.assertEqual(rc, 0)

            # Byte difference (e.g. trailing newline / extra whitespace) must fail
            _write_target(pkg, "rk3568", schema=b'{"type":"object"}\r\n')
            rc, out, _ = self._run_check(pkg)
            self.assertEqual(rc, 1)
            self.assertIn("config.schema.json", out)

            # Present on only one target must fail
            (pkg / "rk3568" / "config.schema.json").unlink()
            rc, out, _ = self._run_check(pkg)
            self.assertEqual(rc, 1)
            self.assertIn("文件只存在于 onnx: config.schema.json", out)

    def test_shared_manifest_metadata_mismatch_fails(self):
        for field, bad_value in (
            ("id", "other_id"),
            ("name", "Different Name"),
            ("version", "2.0.0"),
            ("task_type", "classification"),
            ("category", "traffic"),
        ):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as tmp:
                pkg = Path(tmp) / "demo_pkg"
                _write_target(pkg, "onnx")
                bad_manifest = {
                    "id": "demo_pkg",
                    "name": "Demo Algo",
                    "version": "1.0.0",
                    "task_type": "detection",
                    "category": "safety",
                    "platform": {"arch": "aarch64", "target": "rk3568"},
                }
                bad_manifest[field] = bad_value
                _write_target(pkg, "rk3568", manifest=bad_manifest)
                rc, out, _ = self._run_check(pkg)
                self.assertEqual(rc, 1)
                self.assertIn(f"manifest.json 字段 '{field}' 不一致", out)

    def test_stale_allowed_differences_and_one_sided_fail(self):
        with tempfile.TemporaryDirectory() as tmp:
            pkg = Path(tmp) / "demo_pkg"
            _write_target(pkg, "onnx", src_files={"src/a.cpp": b"same\n", "src/b.cpp": b"onnx\n"})
            _write_target(pkg, "rk3568", src_files={"src/a.cpp": b"same\n", "src/b.cpp": b"rknn\n"})

            # Active allowed diff passes
            rc, _, _ = self._run_check(pkg, allowed_diff={"demo_pkg": {"src/b.cpp": "backend"}})
            self.assertEqual(rc, 0)

            # Identical file in ALLOWED_DIFFERENCES is stale -> fails
            rc, out, _ = self._run_check(
                pkg,
                allowed_diff={"demo_pkg": {"src/a.cpp": "no longer different", "src/b.cpp": "backend"}},
            )
            self.assertEqual(rc, 1)
            self.assertIn("[陈旧]", out)
            self.assertIn("src/a.cpp", out)

            # Nonexistent file in ALLOWED_DIFFERENCES is stale -> fails
            rc, out, _ = self._run_check(
                pkg,
                allowed_diff={"demo_pkg": {"src/ghost.cpp": "deleted", "src/b.cpp": "backend"}},
            )
            self.assertEqual(rc, 1)
            self.assertIn("[陈旧]", out)
            self.assertIn("src/ghost.cpp", out)

            # Present-everywhere file in ALLOWED_ONE_SIDED is stale -> fails
            rc, out, _ = self._run_check(
                pkg,
                allowed_diff={"demo_pkg": {"src/b.cpp": "backend"}},
                allowed_one_sided={"demo_pkg": {"src/a.cpp": "now on both"}},
            )
            self.assertEqual(rc, 1)
            self.assertIn("[陈旧]", out)
            self.assertIn("src/a.cpp", out)

    def test_cli_pass_and_list_allowed(self):
        with tempfile.TemporaryDirectory() as tmp:
            pkg = Path(tmp) / "face_recognition"
            _write_target(
                pkg,
                "onnx",
                src_files={
                    "src/shared.cpp": b"int x = 1;\n",
                    "src/detection/detector.cpp": b"onnx\n",
                    "src/quality/quality_assessor.cpp": b"onnx\n",
                    "src/feature/extractor.cpp": b"onnx\n",
                    "src/attribute/person_attribute.cpp": b"onnx\n",
                    "src/pipeline/face_recognition.cpp": b"onnx\n",
                    "src/common/face_preprocess.hpp": b"onnx only\n",
                },
            )
            _write_target(
                pkg,
                "rk3568",
                src_files={
                    "src/shared.cpp": b"int x = 1;\n",
                    "src/detection/detector.cpp": b"rknn\n",
                    "src/quality/quality_assessor.cpp": b"rknn\n",
                    "src/feature/extractor.cpp": b"rknn\n",
                    "src/attribute/person_attribute.cpp": b"rknn\n",
                    "src/pipeline/face_recognition.cpp": b"rknn\n",
                },
            )
            proc = subprocess.run(
                [sys.executable, str(SCRIPT), str(pkg)],
                capture_output=True,
                text=True,
                timeout=15,
            )
            self.assertEqual(proc.returncode, 0, msg=proc.stdout + proc.stderr)

            list_proc = subprocess.run(
                [sys.executable, str(SCRIPT), str(pkg), "--list-allowed"],
                capture_output=True,
                text=True,
                timeout=15,
            )
            self.assertEqual(list_proc.returncode, 0, msg=list_proc.stdout + list_proc.stderr)
            self.assertIn("src/detection/detector.cpp", list_proc.stdout)
            self.assertIn("src/common/face_preprocess.hpp", list_proc.stdout)


if __name__ == "__main__":
    unittest.main()
