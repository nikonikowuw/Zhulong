#!/usr/bin/env python3
"""
build_package.py - 人员非法聚集检测算法包自动化组装与校验脚本

用法:
    python3 packages/crowd_gathering/scripts/build_package.py [--target onnx] [--output-dir build/packages]
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
    parser = argparse.ArgumentParser(description="组装人员非法聚集检测算法包")
    parser.add_argument("--target", default="onnx", choices=["onnx", "rk3568", "rk3588"], help="目标平台 (默认: onnx)")
    parser.add_argument("--build-dir", default="build/native/host", help="C++ 构建产物目录 (默认: build/native/host)")
    parser.add_argument("--output-dir", default="build/packages", help="输出包目录 (默认: build/packages)")
    parser.add_argument("--no-verify", action="store_true", help="跳过验包验证")
    parser.add_argument("--sdk-prefix", default="build/sdk/prefix",
                        help="算法包 SDK 前缀（默认: build/sdk/prefix，需先 make sdk-install）")
    parser.add_argument("--skip-build", action="store_true",
                        help="跳过包自身可执行文件的构建（复用 packages/<id>/<target>/build/<id>）")
    parser.add_argument("--toolchain", help="交叉构建用的 CMake 工具链文件")
    parser.add_argument("--rknn-root", help="RKNN 前缀")
    parser.add_argument("--onnxruntime-root", help="ONNX Runtime 前缀")
    args = parser.parse_args()
    if args.toolchain and not args.no_verify:
        print("[*] 交叉构建：跳过本机验包（产物在宿主不可执行）。")
        args.no_verify = True

    repo_root = Path(__file__).resolve().parent.parent.parent.parent
    pkg_root = repo_root / "packages" / "crowd_gathering"
    target_dir = pkg_root / args.target

    if not target_dir.exists():
        sys.exit(f"错误: 目标平台目录不存在: {target_dir}")

    manifest_path = target_dir / "manifest.json"
    if not manifest_path.exists():
        sys.exit(f"错误: 缺少 manifest.json: {manifest_path}")

    with open(manifest_path, "r", encoding="utf-8") as f:
        manifest = json.load(f)

    arch = manifest.get("platform", {}).get("arch", "unknown")
    target_name = manifest.get("platform", {}).get("target", args.target)
    pkg_id = manifest.get("id", "crowd_gathering")
    version = manifest.get("version", "1.0.0")

    build_dir = repo_root / args.build_dir

    sdk_prefix = Path(args.sdk_prefix)
    if not sdk_prefix.is_absolute():
        sdk_prefix = repo_root / sdk_prefix
    exe_path = target_dir / "build" / pkg_id
    if not args.skip_build:
        if not sdk_prefix.exists():
            sys.exit(f"错误: SDK 前缀不存在: {sdk_prefix}（先跑 make sdk-install）")
        print(f"[*] 构建包可执行文件（SDK 前缀 {sdk_prefix}）...")
        make_args = ["make", "-C", str(target_dir), "SDK_PREFIX=" + str(sdk_prefix)]
        if args.toolchain:
            make_args.append("TOOLCHAIN=" + str(Path(args.toolchain).resolve()))
        if args.rknn_root:
            make_args.append("RKNN_ROOT=" + str(Path(args.rknn_root).resolve()))
        if args.onnxruntime_root:
            make_args.append("ONNXRUNTIME_ROOT=" + str(Path(args.onnxruntime_root).resolve()))
        res = subprocess.run(make_args)
        if res.returncode != 0:
            sys.exit("错误: 包可执行文件构建失败")
    if not exe_path.exists():
        sys.exit(f"错误: 缺少包可执行文件 {exe_path}（去掉 --skip-build 或先 make）")

    staging_dir = build_dir / "staging" / f"{pkg_id}_{target_name}"
    if staging_dir.exists():
        shutil.rmtree(staging_dir)
    staging_dir.mkdir(parents=True, exist_ok=True)

    # 1. 复制 manifest.json
    shutil.copy2(manifest_path, staging_dir / "manifest.json")

    # 2. 包根可执行文件：文件名必须等于 manifest.id
    dest_entry = staging_dir / pkg_id
    shutil.copy2(exe_path, dest_entry)
    os.chmod(dest_entry, 0o755)
    print(f"[*] 包入口: {dest_entry.name}")

    # 符号门禁：包可执行文件不得导出任何插件 C ABI 符号
    symbol_check = repo_root / "native" / "tests" / "check_plugin_symbols.py"
    if symbol_check.exists() and shutil.which("nm") and not args.no_verify:
        res = subprocess.run([sys.executable, str(symbol_check), "--entry", str(dest_entry)])
        if res.returncode != 0:
            sys.exit("错误: 包入口导出了插件 C ABI（交付形态不得再暴露 dlopen 入口）")

    # 3. 复制 sample.jpg
    sample_src = target_dir / "sample.jpg"
    if not sample_src.exists():
        sys.exit(f"错误: 缺少 sample.jpg: {sample_src}")
    shutil.copy2(sample_src, staging_dir / "sample.jpg")

    # 4. 复制 config.schema.json
    schema_src = target_dir / "config.schema.json"
    if schema_src.exists():
        shutil.copy2(schema_src, staging_dir / "config.schema.json")

    # 5. 复制 models/
    models_src = target_dir / "models"
    if models_src.exists() and models_src.is_dir():
        shutil.copytree(models_src, staging_dir / "models", dirs_exist_ok=True)

    # 6. 本地黑盒验包
    if not args.no_verify and args.target == "onnx":
        print(f"[*] 执行本地黑盒验包（cwd={staging_dir}）...")
        sample_path = staging_dir / "sample.jpg"
        verify_sample = sample_path
        converted = staging_dir / ".verify_sample.ppm"
        ffmpeg = shutil.which("ffmpeg")
        if ffmpeg:
            conv = subprocess.run(
                [ffmpeg, "-v", "error", "-y", "-i", str(sample_path),
                 "-pix_fmt", "rgb24", str(converted)],
                capture_output=True, text=True,
            )
            if conv.returncode == 0 and converted.exists():
                verify_sample = converted
            else:
                print("[!] 警告: sample.jpg 转 PPM 失败，验包将使用合成帧", file=sys.stderr)
        else:
            print("[!] 警告: 未找到 ffmpeg，验包将使用合成帧", file=sys.stderr)

        verify_cmd = [
            str(dest_entry),
            "--verify", "--sample", str(verify_sample),
        ]
        proc = subprocess.run(verify_cmd, capture_output=True, text=True, cwd=str(staging_dir))
        if converted.exists():
            converted.unlink()
        if proc.returncode != 0:
            print(proc.stderr, file=sys.stderr)
            sys.exit(f"错误: 黑盒验包未通过 (退出码 {proc.returncode})")

        last_line = proc.stdout.strip().splitlines()[-1] if proc.stdout.strip() else ""
        if not last_line:
            sys.exit("错误: 黑盒验包没有输出 JSON 报告")
        try:
            report = json.loads(last_line)
        except json.JSONDecodeError as error:
            sys.exit(f"错误: 验包报告不是合法 JSON ({error}): {last_line}")

        if report.get("status") != "passed":
            print(last_line, file=sys.stderr)
            sys.exit(f"错误: 验包报告 status={report.get('status')!r}（期望 passed）")

        # sample_decoded=false 表示包入口回退到合成帧：这时准入只证明了 ABI 生命周期，
        # 真实推理路径一次都没走。按契约必须如实记为告警，不得静默当成通过。
        if not report.get("sample_decoded", False):
            print("[!] 警告: 验包未解码真实样本 (sample_decoded=false)，本次只证明 ABI 生命周期；"
                  "请安装 ffmpeg 或用 PPM 样本复验", file=sys.stderr)
            print(f"[!] 黑盒验包（仅 ABI 冒烟）: {report.get('count', 0)} 条结果，cost_ms={report.get('cost_ms')}")
        else:
            print(f"[✓] 黑盒验包通过！{last_line}")

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
