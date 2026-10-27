#!/usr/bin/env python3
"""把 onnxruntime 的**运行期依赖闭包**拷进 SDK 前缀。

为什么不能只拷 `libonnxruntime.so`：发行版构建的 ORT 不是自包含的，它 NEEDED
`libonnx.so.1` / `libonnx_proto.so.1` / `libprotobuf.so.32` / `libXNNPACK.so*` /
`libpthreadpool.so*` / `libcpuinfo.so*` / `libre2.so*`……在开发机上这些恰好被
apt 装在 `/lib/x86_64-linux-gnu`，于是「拷一个 .so 就够」的假设在干净容器里当场
翻车（链接期 undefined reference，运行期找不到 DT_NEEDED）。

用法：
    bundle_runtime.py --ort-root build/deps/onnxruntime --dest <prefix>/third_party/onnx/lib

glibc / libstdc++ / libgcc / 动态装载器**不**拷：它们必须来自目标系统，拷过去会和
目标机的内核/libc 版本打架。
"""

import argparse
import os
import re
import shutil
import subprocess
import sys

# 目标系统提供的底座：拷进前缀只会和宿主 libc 版本打架
# ⚠️ 必须在 ".so" 处收口：前缀匹配会把 libcpuinfo / libpthreadpool 这类
#    普通依赖当成底座，拷漏之后在干净容器里表现成「找不到 DT_NEEDED」。
SYSTEM_LIBRARIES = re.compile(r"^(ld-linux[^/]*|libc|libm|libdl|libpthread|librt|libresolv|"
                              r"libutil|libstdc\+\+|libgcc_s)\.so")

NEEDED = re.compile(r"\(NEEDED\)\s+Shared library: \[(?P<name>[^\]]+)\]")


def needed_libraries(path, readelf):
    out = subprocess.run([readelf, "-d", path], check=True, capture_output=True, text=True)
    return [m.group("name") for m in NEEDED.finditer(out.stdout)]


def library_search_path():
    dirs = []
    for entry in (os.environ.get("LD_LIBRARY_PATH") or "").split(":"):
        if entry:
            dirs.append(entry)
    try:
        out = subprocess.run(["ldconfig", "-p"], check=True, capture_output=True, text=True)
    except (OSError, subprocess.CalledProcessError):
        out = None
    if out is not None:
        for line in out.stdout.splitlines():
            match = re.search(r"=>\s*(?P<path>\S+)$", line)
            if match:
                dirs.append(os.path.dirname(match.group("path")))
    # 去重但保序
    seen = set()
    return [d for d in dirs if not (d in seen or seen.add(d))]


def resolve(soname, known_dirs):
    for directory in known_dirs:
        candidate = os.path.join(directory, soname)
        if os.path.exists(candidate):
            return os.path.realpath(candidate)
    return None


def collect(root, seed_names, readelf):
    """广度优先解析 NEEDED 闭包，只在 root 与系统搜索路径里找。"""
    known_dirs = [root] + library_search_path()
    resolved = {}
    queue = list(seed_names)
    missing = []
    while queue:
        soname = queue.pop()
        if soname in resolved or soname in missing:
            continue
        if SYSTEM_LIBRARIES.match(soname):
            # 底座库既不拷也不再展开依赖：目标机必须用自己那一套
            resolved[soname] = None
            continue
        path = resolve(soname, known_dirs)
        if path is None:
            missing.append(soname)
            continue
        resolved[soname] = path
        for dep in needed_libraries(path, readelf):
            if dep not in resolved:
                queue.append(dep)
    return resolved, missing


def main(argv):
    parser = argparse.ArgumentParser(description="拷贝 onnxruntime 依赖闭包到 SDK 前缀")
    parser.add_argument("--ort-root", required=True)
    parser.add_argument("--dest", required=True)
    parser.add_argument("--readelf", default="readelf")
    args = parser.parse_args(argv)

    ort_root = os.path.abspath(args.ort_root)
    seeds = sorted(name for name in os.listdir(ort_root) if name.startswith("libonnxruntime.so"))
    if not seeds:
        sys.stderr.write(f"错误: {ort_root} 里没有 libonnxruntime.so\n")
        return 1

    resolved, missing = collect(ort_root, seeds, args.readelf)
    if missing:
        sys.stderr.write(
            "错误: 以下依赖在系统里找不到，前缀不完整（装一下对应的 -dev/运行库包）:\n  "
            + "\n  ".join(missing) + "\n"
        )
        return 1

    os.makedirs(args.dest, exist_ok=True)
    copied = []
    for soname, path in sorted(resolved.items()):
        if path is None:
            continue
        target = os.path.join(args.dest, soname)
        shutil.copyfile(path, target)
        os.chmod(target, 0o755)
        copied.append(soname)

    print(f"[✓] 已拷入 {len(copied)} 个运行库到 {args.dest}")
    for name in copied:
        print(f"    {name}")
    system = sorted(name for name, path in resolved.items() if path is None)
    if system:
        print("[i] 由目标系统提供（按要求不打包）: " + ", ".join(system))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
