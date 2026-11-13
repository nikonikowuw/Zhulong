#!/usr/bin/env python3
"""
check_package_drift.py - 多 target 算法包的源码漂移检查

一个包按 target 分家（`packages/<id>/<target>/`）时，每个档位是一份**自包含**的包副本，
差异应当只有「后端决定的那几行」。现实是同一处逻辑修复常常只落到一个档位，另一份悄悄
变成孤儿——而两份都能编译通过，问题只在板端表现为「这台机器结果不对」。

用法:
    python3 native/scripts/check_package_drift.py packages/face_recognition
    python3 native/scripts/check_package_drift.py packages/face_recognition --list-allowed

判据:
    - 比较范围是 `src/**` 与 `tests/**`（构建脚本/CMake 工程按档位本就不同）；
    - 出现在 ALLOWED_DIFFERENCES 里的相对路径允许内容不同，但**必须两边都存在**；
    - 未登记的差异 = 漂移，退出码 1，并打印两侧统一的 diff。
"""

import argparse
import difflib
import json
import sys
from pathlib import Path

# 允许按 target 不同的文件：相对包目录的路径 → 允许不同的理由。
# ⚠️ 新增条目等于宣布「这段逻辑允许两份实现」，请在 PR 里写清为什么不能用同一份。
ALLOWED_DIFFERENCES = {
    "face_recognition": {
        "src/detection/detector.cpp": "前处理不同：ONNX 需 HWC→NCHW float32，RKNN 模型自带归一化",
        "src/quality/quality_assessor.cpp": "前处理不同：同上（质量评估输入）",
        "src/feature/extractor.cpp": "前处理不同：同上（特征提取输入）",
        "src/pipeline/face_recognition.cpp": "权重扩展名与开发期源码树前缀按 target 不同",
    },
}

# 允许只存在于部分 target 的文件（缺侧不算漂移）。
ALLOWED_ONE_SIDED = {
    "face_recognition": {
        "src/common/face_preprocess.hpp": "仅「模型不自带归一化」的档位需要该前处理助手",
    },
}

COMPARE_DIRECTORIES = ("src", "tests")


def target_directories(package_dir: Path) -> list[Path]:
    """返回该包下所有档位目录（含 manifest.json 且带 platform.target 的子目录）。"""
    targets = []
    for candidate in sorted(package_dir.iterdir()):
        manifest = candidate / "manifest.json"
        if not candidate.is_dir() or not manifest.is_file():
            continue
        try:
            data = json.loads(manifest.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as error:
            print(f"[!] 跳过无法解析的 manifest: {manifest}: {error}", file=sys.stderr)
            continue
        if data.get("platform", {}).get("target"):
            targets.append(candidate)
    return targets


def collect_files(target_dir: Path) -> dict[str, Path]:
    files: dict[str, Path] = {}
    for directory in COMPARE_DIRECTORIES:
        root = target_dir / directory
        if not root.is_dir():
            continue
        for path in sorted(root.rglob("*")):
            if path.is_file():
                files[path.relative_to(target_dir).as_posix()] = path
    return files


def main() -> int:
    parser = argparse.ArgumentParser(description="算法包多 target 源码漂移检查")
    parser.add_argument("package_dir", type=Path, help="包目录，如 packages/face_recognition")
    parser.add_argument("--list-allowed", action="store_true", help="只打印允许差异清单")
    args = parser.parse_args()

    package_dir: Path = args.package_dir
    if not package_dir.is_dir():
        sys.exit(f"错误: 包目录不存在: {package_dir}")

    package_id = package_dir.name
    allowed_diff = ALLOWED_DIFFERENCES.get(package_id, {})
    allowed_one_sided = ALLOWED_ONE_SIDED.get(package_id, {})

    if args.list_allowed:
        print(f"{package_id} 允许按 target 不同的文件：")
        for path, reason in allowed_diff.items():
            print(f"  {path}: {reason}")
        for path, reason in allowed_one_sided.items():
            print(f"  {path}（单侧）: {reason}")
        return 0

    targets = target_directories(package_dir)
    if len(targets) < 2:
        print(f"[✓] {package_id}: 只有一个 target（{targets[0].name if targets else '无'}），无需漂移检查")
        return 0

    reference = targets[0]
    reference_files = collect_files(reference)
    drift_found = False

    print(f"[*] {package_id}: 以 {reference.name} 为基准比较 {', '.join(t.name for t in targets[1:])}")

    for other in targets[1:]:
        other_files = collect_files(other)

        for relative_path in sorted(set(reference_files) | set(other_files)):
            in_reference = relative_path in reference_files
            in_other = relative_path in other_files

            if in_reference != in_other:
                if relative_path in allowed_one_sided:
                    continue
                drift_found = True
                side = reference.name if in_reference else other.name
                print(f"\n[漂移] 文件只存在于 {side}: {relative_path}")
                continue

            if relative_path in allowed_diff:
                continue

            reference_text = reference_files[relative_path].read_text(encoding="utf-8", errors="replace")
            other_text = other_files[relative_path].read_text(encoding="utf-8", errors="replace")
            if reference_text == other_text:
                continue

            drift_found = True
            print(f"\n[漂移] {relative_path}（未登记为允许差异）")
            diff = difflib.unified_diff(
                reference_text.splitlines(),
                other_text.splitlines(),
                fromfile=f"{reference.name}/{relative_path}",
                tofile=f"{other.name}/{relative_path}",
                lineterm="",
            )
            for index, line in enumerate(diff):
                if index >= 40:
                    print("  ...（diff 已截断）")
                    break
                print(f"  {line}")

    if drift_found:
        print(
            "\n错误: 检测到未登记的 target 间漂移。\n"
            "  要么把两边改成一致，要么在 check_package_drift.py 的 ALLOWED_DIFFERENCES 里\n"
            "  登记路径与理由（等于宣布这段逻辑必须有两份实现）。",
            file=sys.stderr,
        )
        return 1

    print(f"[✓] {package_id}: 各 target 源码一致（允许差异 {len(allowed_diff)} 项）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
