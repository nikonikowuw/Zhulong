#!/usr/bin/env python3
"""
build_package.py - 车牌识别算法包自动化组装与校验脚本

用法:
    python3 packages/plate_recognition/scripts/build_package.py [--target cpu|rk3568|rk3588] [--output-dir build/packages]
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import tarfile
from pathlib import Path

def main():
    parser = argparse.ArgumentParser(description="组装车牌识别算法包")
    parser.add_argument("--target", default="cpu", choices=["cpu", "rk3568", "rk3588"], help="目标平台 (默认: cpu)")
    parser.add_argument("--build-dir", default="build/native/host", help="C++ 构建产物目录 (默认: build/native/host)")
    parser.add_argument("--output-dir", default="build/packages", help="输出包目录 (默认: build/packages)")
    parser.add_argument("--no-verify", action="store_true", help="跳过验包验证")
    args = parser.parse_args()

    repo_root = Path(__file__).resolve().parent.parent.parent.parent
    pkg_root = repo_root / "packages" / "plate_recognition"
    target_dir = pkg_root / "targets" / args.target

    if not target_dir.exists():
        sys.exit(f"错误: 目标平台目录不存在: {target_dir}")

    manifest_path = target_dir / "manifest.json"
    if not manifest_path.exists():
        sys.exit(f"错误: 缺少 manifest.json: {manifest_path}")

    with open(manifest_path, "r", encoding="utf-8") as f:
        manifest = json.load(f)

    arch = manifest.get("platform", {}).get("arch", "unknown")
    target_name = manifest.get("platform", {}).get("target", args.target)
    pkg_id = manifest.get("id", "plate_recognition")
    version = manifest.get("version", "1.0.0")

    build_dir = repo_root / args.build_dir
    possible_so = [
        build_dir / "libPlateRecognitionPlugin.so",
        build_dir / "PlateRecognitionPlugin.so",
    ]
    plugin_so = None
    for p in possible_so:
        if p.exists():
            plugin_so = p
            break

    if not plugin_so:
        print(f"未找到已编译的插件 .so，尝试调用 cmake 构建...")
        res = subprocess.run(["cmake", "--build", str(build_dir), "--target", "PlateRecognitionPlugin"], cwd=str(repo_root))
        if res.returncode != 0:
            sys.exit("错误: CMake 构建 PlateRecognitionPlugin 失败！")
        for p in possible_so:
            if p.exists():
                plugin_so = p
                break
        if not plugin_so:
            sys.exit(f"错误: 构建后仍未找到插件库文件")

    print(f"[*] 使用插件库: {plugin_so}")

    staging_dir = build_dir / "staging" / f"{pkg_id}_{target_name}"
    if staging_dir.exists():
        shutil.rmtree(staging_dir)
    staging_dir.mkdir(parents=True, exist_ok=True)

    # 1. 复制 manifest.json
    shutil.copy2(manifest_path, staging_dir / "manifest.json")

    # 2. 复制 plugin.so 并赋予 0755
    dest_plugin = staging_dir / "plugin.so"
    shutil.copy2(plugin_so, dest_plugin)
    os.chmod(dest_plugin, 0o755)

    # 3. 复制 sample.jpg
    sample_src = pkg_root / "sample.jpg"
    if not sample_src.exists():
        sys.exit(f"错误: 缺少 sample.jpg: {sample_src}")
    shutil.copy2(sample_src, staging_dir / "sample.jpg")

    # 4. 复制 config.schema.json
    schema_src = pkg_root / "config.schema.json"
    if schema_src.exists():
        shutil.copy2(schema_src, staging_dir / "config.schema.json")

    # 5. 复制 models/
    models_src = target_dir / "models"
    if models_src.exists() and models_src.is_dir():
        shutil.copytree(models_src, staging_dir / "models", dirs_exist_ok=True)

    # 6. 本地黑盒验包
    runner_bin = build_dir / "zhulong_algo_runner"
    if not args.no_verify and runner_bin.exists() and args.target == "cpu":
        print(f"[*] 执行本地黑盒验包...")
        verify_cmd = [
            str(runner_bin),
            "--verify-plugin", str(dest_plugin),
            "--sample", str(staging_dir / "sample.jpg")
        ]
        proc = subprocess.run(verify_cmd, capture_output=True, text=True)
        if proc.returncode != 0:
            print(proc.stderr, file=sys.stderr)
            sys.exit(f"错误: 黑盒验包未通过 (退出码 {proc.returncode})")
        print(f"[✓] 黑盒验包通过！输出: {proc.stdout.strip()}")

    # 7. 打包成 .tar.gz
    out_dir = repo_root / args.output_dir
    out_dir.mkdir(parents=True, exist_ok=True)

    archive_filename = f"{pkg_id}-{arch}-{target_name}.tar.gz"
    out_tar_path = out_dir / archive_filename
    print(f"[*] 正在打包至 {out_tar_path}...")

    with tarfile.open(out_tar_path, "w:gz") as tar:
        for item in sorted(staging_dir.iterdir()):
            tar.add(item, arcname=item.name)

    print(f"[✓] 算法包构建完成: {out_tar_path}")

if __name__ == "__main__":
    main()
