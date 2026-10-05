#!/usr/bin/env python3
"""增量构建与 CGO 外部静态库链接验证脚本

验证目标（⚠️ 解决 Go CGO 缓存陈旧的常见坑）：
  Go 的构建缓存默认不会深度探测外部静态归档（.a）的内容变化，容易导致在 Native C++ 代码
  修改后，Go 误复用旧的链接产物。
  本脚本通过主动向 ABI 源文件临时注入内容标记，执行端到端增量构建验证：
  1. 验证修改 Native 源码后，只会增量重新编译对应的 .o 并重新链接 Engine 静态库；
  2. 验证 FFmpeg 依赖库不会被误重编；
  3. 验证 Go 最终生成的二进制产物中确实包含新的标记内容（未命中陈旧缓存）；
  4. 验证源文件安全复原后，临时标记彻底消失。
"""
import hashlib
import json
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
BUILD = ROOT / "build/native/host"
OUTPUT = ROOT / "build/validation"
SOURCE = ROOT / "native/src/abi/engine.cpp"
MARKER = b"Zhulong_native_incremental_verification_marker"


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def snapshot(paths):
    """记录路径集合的文件内容哈希与修改时间戳快照"""
    return {str(path.relative_to(ROOT)): {"sha256": sha(path), "mtime_ns": path.stat().st_mtime_ns}
            for path in paths}


def build_binary(name):
    """通过 native 构建驱动调用 Go 外部链接构建二进制"""
    output = OUTPUT / name
    subprocess.run([sys.executable, str(ROOT / "native/scripts/build.py"), "go", "build",
                    "-ldflags=-linkmode=external", "-o", str(output), "./cmd/Zhulong"],
                   cwd=ROOT, check=True, timeout=180)
    return output


def main():
    if sys.argv[1:] != ["--allow-source-edit"]:
        sys.exit("requires --allow-source-edit and exclusive writer ownership")

    OUTPUT.mkdir(parents=True, exist_ok=True)
    original = SOURCE.read_bytes()
    # 构造含临时导出符号的修改内容
    modified = original + b'\nextern "C" const char *Zhulong_incremental_check() { return "' + MARKER + b'"; }\n'

    if MARKER in original:
        sys.exit("source already contains verification marker; refusing edit")

    # 1. 构建修改前的基准二进制并确认不含标记
    baseline = build_binary("Zhulong-before")
    assert MARKER not in baseline.read_bytes()

    # 记录修改前的各库与目标文件快照
    libraries = list((ROOT / "build/deps/ffmpeg/host").glob("*/install/lib/*.a"))
    objects = list((BUILD / "CMakeFiles/ZhulongEngine.dir/src").rglob("*.o"))
    ffmpeg_before = snapshot(libraries)
    objects_before = snapshot(objects)
    link_before = json.loads((BUILD / "go-link.json").read_text())

    # 2. 注入修改标记
    SOURCE.write_bytes(modified)
    try:
        # 构建修改后的二进制并验证
        changed = build_binary("Zhulong-modified")
        assert MARKER in changed.read_bytes(), "Go reused a stale external archive"
        assert ffmpeg_before == snapshot(libraries), "Engine edit rebuilt FFmpeg"

        objects_after = snapshot(objects)
        recompiled = [name for name in objects_before if objects_before[name] != objects_after[name]]
        assert recompiled == ["build/native/host/CMakeFiles/ZhulongEngine.dir/src/abi/engine.cpp.o"], recompiled

        link_after = json.loads((BUILD / "go-link.json").read_text())
        assert link_before["CGO_LDFLAGS"] != link_after["CGO_LDFLAGS"]

        evidence = dict(recompiled_objects=recompiled, ffmpeg_unchanged=True,
                        modified_binary_contains_marker=True, cgo_link_inputs_changed=True,
                        baseline_binary=str(baseline), modified_binary=str(changed))
    finally:
        # 3. 无论成功或失败，安全复原源码
        if SOURCE.read_bytes() != modified:
            sys.exit("source changed concurrently: refusing to overwrite; restore manually from verification context")
        SOURCE.write_bytes(original)
        restored = build_binary("Zhulong-final")
        assert MARKER not in restored.read_bytes(), "restored binary still contains temporary code"

    evidence["restored_binary"] = str(restored)
    evidence["source_restored"] = SOURCE.read_bytes() == original
    (OUTPUT / "incremental-evidence.json").write_text(json.dumps(evidence, indent=2) + "\n")
    print(json.dumps(evidence, indent=2))


if __name__ == "__main__":
    main()
