#!/usr/bin/env python3
"""Zhulong Native 引擎与静态 FFmpeg 离线构建驱动脚本

核心设计与安全规范：
1. 默认严格离线（Offline-by-default）：
   - 依赖下载必须由显式命令触发（如 make native-deps），其余配置与编译命令杜绝任何隐式网络请求；
   - 校验依赖源码的固定版本与完整 SHA-256 指纹。
2. 内容寻址与 CGO 缓存失效保护（Content-addressed CGO link input）：
   - Go 编译器对外部归档（.a）的时间戳与内容变化不敏感，极易导致 CGO 复用旧二进制；
   - 本脚本基于参与 CGO 静态链接的 Zhulong .a 归档完整 SHA-256 哈希值建立隔离的链接目录，
     动态修改 CGO_LDFLAGS 链接参数，确保 Go 编译器 100% 能够感知 Native 代码变动。
3. 纯洁的跨架构与交叉编译支持：
   - 严禁读取宿主机未受控的环境变量（如 CPATH、C_INCLUDE_PATH、LIBRARY_PATH 等）；
   - 严格校验 Compiler Target Triple 与 Go 目标架构的一致性，拒绝 Host/Cross 库混杂。
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess
import sys
import tarfile
import urllib.request

# 项目根目录与固定依赖版本配置
ROOT = Path(__file__).resolve().parents[2]
VERSION = "7.1.5"
SHA256 = "de668509caf9e35e3cd162473441fdb29538c6d96ed080292b3cf9e6fc5d558f"
ARCHIVE = ROOT / "build/deps/sources" / f"ffmpeg-{VERSION}.tar.xz"

# Vendored RKNN 供应商前缀（离线，不联网）。仅 RKNN 推理后端构建使用。
RKNN_VENDOR = ROOT / "native/vendor/rknpu2"
RKNN_SHA256 = {
    "include/rknn_api.h": "c48e11a6f41b451a5fd1e4ad774ea60252d3d94f78bee9b21ea3d21b21deba9a",
    "lib/librknnrt.so":   "d31fc19c85b85f6091b2bd0f6af9d962d5264a4e410bfb536402ec92bac738e8",
}

# Vendored RGA 供应商前缀（离线）。仅 RGA 视觉前处理后端构建使用。
# 版本三元组：库 API 1.10.1_[10] / 内核驱动 v1.3.4 / byte_stride_align 4（RK3568 实测）。
RGA_VENDOR = ROOT / "native/vendor/rga"
RGA_SHA256 = {
    "include/rga/im2d.h": "5ee06891775451c56e6264f5d53db0a4e948e1830287f3a5db4f75c30ac0ff11",
    "include/rga/rga.h":  "f50214c9196cfdb1a66301d71d53cd335ed4bc8b8cdf5a56fdcce5965db2c6b9",
    "lib/librga.so.2.1.0": "ba8744397f126c87dc2d637ddea35cca562aa8f323631bb4b9883816b3dcb714",
}

# FFmpeg 最小化安全裁剪参数（纯静态、位置无关代码 PIC、仅开启 RTSP 与 H264/H265 解码所需组件）
#
# 许可证警示：切勿加入 --enable-gpl / --enable-nonfree / --enable-version3。
# 当前配置产出 LGPL 2.1 or later，这是 Zhulong 以 GPL-3.0 分发（静态链接）得以成立的前提。
# 一旦启用 --enable-gpl（引入 x264/x265 等 GPL 编码器），整包即变为 GPL，
# 合规判断随之失效。详见 THIRD-PARTY.md。
FLAGS = [
    "--disable-autodetect", "--disable-everything", "--disable-programs",
    "--disable-doc", "--disable-avdevice", "--disable-avfilter", "--disable-swscale",
    "--disable-swresample", "--disable-postproc", "--disable-iconv", "--disable-shared",
    "--enable-static", "--enable-pic", "--enable-network", "--enable-avformat",
    "--enable-avcodec", "--enable-avutil", "--enable-demuxer=rtsp",
    "--enable-protocol=tcp,udp,rtp", "--enable-parser=h264,hevc",
    "--enable-decoder=h264,hevc"
]
PACKAGE_RESOLVER = ROOT / "native/scripts/no_external_pkg_config.sh"


def run(args, **kwargs):
    """打印并执行命令，命令失败时抛出异常"""
    print("+ " + shlex.join(map(str, args)), flush=True)
    subprocess.run(list(map(str, args)), check=True, **kwargs)


def output(args):
    """执行命令并获取标准输出字符串"""
    return subprocess.check_output(args, text=True).strip()


def digest(path):
    """计算单个文件的 SHA-256 哈希值"""
    h = hashlib.sha256()
    with open(path, "rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def tool(name):
    """在系统 PATH 中解析编译工具的绝对路径"""
    path = shutil.which(name)
    if not path:
        raise ValueError(f"required tool not found: {name}; no toolchain is downloaded")
    return str(Path(path).resolve())


def tree_digest(path):
    """递归计算整个目录树的内容哈希指纹（用于 sysroot/SDK 变动检测）

    安全约束：
      检查目录树中是否存在逃逸出根路径的软链接，防止交叉编译意外读取宿主机系统库。
    """
    h = hashlib.sha256()
    root = Path(path).resolve()
    for item in sorted(root.rglob("*")):
        h.update(str(item.relative_to(root)).encode())
        if item.is_symlink():
            if not item.resolve().is_relative_to(root):
                raise ValueError(f"sysroot symlink escapes target tree: {item}")
            h.update(os.readlink(item).encode())
            if item.is_file():
                h.update(digest(item).encode())
        elif item.is_file():
            h.update(digest(item).encode())
    return h.hexdigest()


def sanitize_environment():
    """净化环境：对 macOS 系统 xcrun 自动注入的默认环境变量进行清理

    macOS 下使用 /usr/bin/python3 或 xcrun 启动时，Apple 会隐式注入：
      CPATH=/usr/local/include, LIBRARY_PATH=/usr/local/lib 以及标准 SDKROOT。
    清理这些系统默认注入，防止误伤正常本地开发环境，同时保证编译器隔离纯洁性。
    """
    if platform.system() == "Darwin":
        if os.environ.get("CPATH") == "/usr/local/include":
            os.environ.pop("CPATH", None)
        if os.environ.get("LIBRARY_PATH") == "/usr/local/lib":
            os.environ.pop("LIBRARY_PATH", None)
        sdkroot = os.environ.get("SDKROOT", "")
        if sdkroot.startswith(("/Library/Developer/", "/Applications/Xcode.app/")):
            os.environ.pop("SDKROOT", None)


def profile(path):
    """解析并校验目标构建 Profile（Host 或交叉编译环境）

    构建环境净化：
      严格拒绝外部注入的环境变量（如 CFLAGS/CPATH 等），确保构建行为完全由 profile 显式确定。
    """
    sanitize_environment()
    for key in ("CFLAGS", "CXXFLAGS", "CPPFLAGS", "LDFLAGS", "CPATH", "C_INCLUDE_PATH",
                "CPLUS_INCLUDE_PATH", "OBJC_INCLUDE_PATH", "LIBRARY_PATH", "COMPILER_PATH",
                "GCC_EXEC_PREFIX", "SDKROOT", "MACOSX_DEPLOYMENT_TARGET", "CGO_CFLAGS",
                "CGO_CXXFLAGS", "CGO_CPPFLAGS", "CGO_LDFLAGS"):
        if os.environ.get(key):
            raise ValueError(f"use explicit toolchain/profile flags instead of ambient {key}")

    if path:
        # 解析交叉编译配置 JSON 文件
        p = json.loads(Path(path).read_text())
        required = {"name", "cc", "cxx", "ar", "ranlib", "sysroot", "arch", "goarch", "target_os"}
        if required - p.keys():
            raise ValueError("cross profile missing: " + ", ".join(sorted(required - p.keys())))
        if p["target_os"] != "linux" or p["goarch"] not in ("arm64", "arm", "amd64"):
            raise ValueError("cross profiles currently support explicit Linux arm64/arm/amd64 targets")
        if not p["name"].replace("-", "").replace("_", "").isalnum():
            raise ValueError("invalid profile name")
        p["mode"] = "cross-" + p["name"]
        p["sysroot"] = str(Path(p["sysroot"]).resolve())
        if not Path(p["sysroot"]).is_dir() or p["sysroot"] == "/":
            raise ValueError("cross build requires a separate existing target sysroot")
        p["sysroot_sha256"] = tree_digest(p["sysroot"])
        if p["goarch"] == "arm" and p.get("goarm") not in ("5", "6", "7"):
            raise ValueError("32-bit ARM requires explicit goarm (5, 6, or 7)")
    else:
        # 解析本地 Host 构建配置
        machine = platform.machine()
        p = dict(name="host", mode="host", cc=os.environ.get("CC", "cc"),
                 cxx=os.environ.get("CXX", "c++"), ar="ar", ranlib="ranlib",
                 arch=machine, goarch={"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[machine],
                 target_os=platform.system().lower())

    for name in ("cc", "cxx", "ar", "ranlib"):
        p[name] = tool(p[name])

    # 记录工具链版本与二进制指纹
    versions = {}
    for name in ("cc", "cxx", "ar", "ranlib"):
        result = subprocess.run([p[name], "--version"], text=True, capture_output=True, check=False)
        versions[name] = result.stdout + result.stderr
    p["tools"] = {name: {"sha256": digest(p[name]), "version": versions[name]}
                  for name in ("cc", "cxx", "ar", "ranlib")}

    # 深度指纹化编译器核心后端文件（如 cc1/cc1plus/libgcc.a/libstdc++.so），防止就地热替换编译器逃避缓存
    p["compiler_inputs"] = {}
    for compiler, query in (("cc", "-print-prog-name=cc1"), ("cxx", "-print-prog-name=cc1plus"),
                            ("cc", "-print-file-name=libgcc.a"), ("cxx", "-print-file-name=libstdc++.so")):
        result = subprocess.run([p[compiler], query], text=True, capture_output=True, check=False)
        candidate = Path(result.stdout.strip())
        if result.returncode == 0 and candidate.is_file():
            p["compiler_inputs"][str(candidate.resolve())] = digest(candidate)

    # 校验 C 与 C++ 编译器的 Target Triple 必须一致
    p["triple"] = output([p["cc"], "-dumpmachine"])
    if output([p["cxx"], "-dumpmachine"]) != p["triple"]:
        raise ValueError("C and C++ target triples differ")

    expected = {"arm64": ("aarch64", "arm64"), "arm": ("arm",), "amd64": ("x86_64",)}[p["goarch"]]
    if not p["triple"].startswith(expected):
        raise ValueError("compiler target does not match Go target; refusing mixed libraries")

    architectures = {"arm64": ("aarch64", "arm64"), "arm": ("arm",), "amd64": ("x86_64", "amd64")}
    if p["arch"] not in architectures[p["goarch"]]:
        raise ValueError("FFmpeg architecture does not match Go target")

    p.setdefault("cflags", [])
    p.setdefault("ldflags", [])

    # 后端选择（编译期 profile 开关）：推理与视觉前处理两个正交维度。
    # 优先级：profile JSON 显式值 → 环境变量 ZHULONG_<KEY> → cpu。
    # 不往 identity 里叠加无意义的失效（见 dependency()），也不默认写入未声明的键。
    #
    # ⚠️ 环境变量这一层是必需的：`make` 与 Air（.air.toml 的 build cmd）各自独立调用
    #    build.py，而每次调用都会重新 configure CMake。只在命令行传
    #    `--inference-backend onnx` 无法覆盖 Air 内部那次无参调用——它会把
    #    ZHULONG_INFERENCE_BACKEND 重置回 cpu 并重链出桩后端。
    for key, allowed in (("inference_backend", ("cpu", "rknn", "onnx")), ("vision_backend", ("cpu", "rga"))):
        env_key = "ZHULONG_" + key.upper()
        value = p.get(key) or os.environ.get(env_key) or "cpu"
        if value not in allowed:
            raise ValueError(f"invalid {key}: {value!r}; expected one of {allowed}")
        p[key] = value
    return p


def rknn_prefix():
    """校验 vendored RKNN 前缀（离线，不联网）

    逐个校验 RKNN_SHA256 里的文件存在且 digest 匹配；不匹配抛 ValueError。
    返回 RKNN_VENDOR。
    """
    for relative, expected in RKNN_SHA256.items():
        candidate = RKNN_VENDOR / relative
        if not candidate.is_file():
            raise ValueError(f"vendored RKNN prefix missing {relative}; expected under {RKNN_VENDOR}")
        actual = digest(candidate)
        if actual != expected:
            raise ValueError(f"vendored RKNN {relative} checksum mismatch: {actual} != {expected}")
    return RKNN_VENDOR


ONNX_VENDOR = ROOT / "native/vendor/onnxruntime"
ONNX_DEPS = ROOT / "build/deps/onnxruntime"


def onnx_prefix(explicit_root=None):
    """解析并校验 ONNX Runtime 前缀目录（离线，不联网）

    优先级：显式参数 (--onnx-root / profile.onnx_root) > 环境变量 ZHULONG_ONNX_ROOT >
    native/vendor/onnxruntime > build/deps/onnxruntime。
    校验 include/onnxruntime_cxx_api.h 与 lib/libonnxruntime.so 必须存在。
    """
    candidates = []
    if explicit_root:
        candidates.append(Path(explicit_root))
    env_root = os.environ.get("ZHULONG_ONNX_ROOT")
    if env_root:
        candidates.append(Path(env_root))
    if not candidates:
        for default_dir in (ONNX_VENDOR, ONNX_DEPS):
            if (default_dir / "include/onnxruntime_cxx_api.h").is_file() and (default_dir / "lib/libonnxruntime.so").is_file():
                candidates.append(default_dir)
                break
    if not candidates:
        raise ValueError("ONNX Runtime prefix not found; pass --onnx-root or set ZHULONG_ONNX_ROOT")

    prefix = candidates[0].resolve()
    header = prefix / "include/onnxruntime_cxx_api.h"
    library = prefix / "lib/libonnxruntime.so"
    if not header.is_file():
        raise ValueError(f"ONNX Runtime prefix missing include/onnxruntime_cxx_api.h: {prefix}")
    if not library.is_file():
        raise ValueError(f"ONNX Runtime prefix missing lib/libonnxruntime.so: {prefix}")
    return prefix


def rga_prefix():
    """校验 vendored RGA 前缀（离线，不联网）

    逐个校验 RGA_SHA256 里的文件存在且 digest 匹配；不匹配抛 ValueError。
    返回 RGA_VENDOR。
    """
    for relative, expected in RGA_SHA256.items():
        candidate = RGA_VENDOR / relative
        if not candidate.is_file():
            raise ValueError(f"vendored RGA prefix missing {relative}; expected under {RGA_VENDOR}")
        actual = digest(candidate)
        if actual != expected:
            raise ValueError(f"vendored RGA {relative} checksum mismatch: {actual} != {expected}")
    return RGA_VENDOR


def prepare(local, download):
    """校验或下载固定的 FFmpeg 源码压缩包"""
    ARCHIVE.parent.mkdir(parents=True, exist_ok=True)
    if local:
        if digest(local) != SHA256:
            raise ValueError("FFmpeg archive checksum mismatch")
        if Path(local).resolve() != ARCHIVE.resolve():
            shutil.copyfile(local, ARCHIVE)
    elif download and not ARCHIVE.exists():
        temporary = ARCHIVE.with_suffix(".download")
        try:
            urllib.request.urlretrieve(f"https://ffmpeg.org/releases/ffmpeg-{VERSION}.tar.xz", temporary)
            if digest(temporary) != SHA256:
                raise ValueError("FFmpeg archive checksum mismatch")
            temporary.replace(ARCHIVE)
        finally:
            temporary.unlink(missing_ok=True)
    if not ARCHIVE.exists() or digest(ARCHIVE) != SHA256:
        raise ValueError("verified FFmpeg source missing; run make native-deps (explicit network) or prepare --archive PATH")
    print(f"verified {ARCHIVE}: {SHA256}")


def dependency(p):
    """构建与缓存静态裁剪版 FFmpeg 依赖

    基于源码哈希、编译参数、编译器版本及工具链指纹生成唯一 Identity 缓存键。
    若缓存已存在则直接命中，杜绝重复编译。
    """
    flags = list(FLAGS)
    if p["arch"] in ("x86_64", "amd64"):
        flags.append("--disable-x86asm")

    identity = dict(
        source_sha256=SHA256, version=VERSION, flags=flags,
        # 后端选择键不影响 FFmpeg 配置，过滤掉以避免无谓的缓存失效。
        profile={k: v for k, v in p.items() if k not in ("inference_backend", "vision_backend", "onnx_root")},
        recipe_sha256=digest(__file__), package_resolver_sha256=digest(PACKAGE_RESOLVER), patches=[]
    )
    key = hashlib.sha256(json.dumps(identity, sort_keys=True).encode()).hexdigest()[:24]
    base = ROOT / "build/deps/ffmpeg" / p["mode"] / key
    prefix = base / "install"
    marker = prefix / "zhulong-ffmpeg.json"

    if marker.exists():
        if json.loads(marker.read_text()) != identity:
            raise ValueError("dependency manifest identity mismatch")
        for lib in ("avformat", "avcodec", "avutil"):
            if not (prefix / "lib" / f"lib{lib}.a").exists():
                raise ValueError("incomplete FFmpeg cache; remove this identity directory and rebuild")
        print(f"FFmpeg cache hit: {prefix}")
        return prefix

    # 准备解包并编译
    prepare(None, False)
    base.mkdir(parents=True, exist_ok=True)
    source = base / f"ffmpeg-{VERSION}"
    if not source.exists():
        with tarfile.open(ARCHIVE) as archive:
            if hasattr(tarfile, "data_filter"):
                archive.extractall(base, filter="data")
            else:
                archive.extractall(base)

    work = base / "objects"
    work.mkdir(exist_ok=True)
    args = [
        source / "configure", f"--prefix={prefix}", *flags, f"--pkg-config={PACKAGE_RESOLVER}",
        f"--cc={p['cc']}", f"--cxx={p['cxx']}", f"--ar={p['ar']}", f"--ranlib={p['ranlib']}",
        "--extra-cflags=" + shlex.join(p["cflags"]), "--extra-ldflags=" + shlex.join(p["ldflags"])
    ]
    if p["mode"] != "host":
        args += ["--enable-cross-compile", f"--target-os={p['target_os']}",
                 f"--arch={p['arch']}", f"--sysroot={p['sysroot']}"]

    with open(base / "build.log", "w") as log:
        run(args, cwd=work, stdout=log, stderr=subprocess.STDOUT)
        run(["make", "-j", str(min(os.cpu_count() or 2, 8))], cwd=work, stdout=log, stderr=subprocess.STDOUT)
        run(["make", "install"], cwd=work, stdout=log, stderr=subprocess.STDOUT)

    notices = prefix / "share/zhulong"
    notices.mkdir(parents=True, exist_ok=True)
    for name in ("COPYING.LGPLv2.1", "LICENSE.md"):
        shutil.copyfile(source / name, notices / name)
    shutil.copyfile(work / "config.h", notices / "config.h")
    shutil.copyfile(work / "ffbuild/config.mak", notices / "config.mak")
    marker.write_text(json.dumps(identity, indent=2, sort_keys=True) + "\n")
    return prefix


ALLOWED_FRAMEWORKS = {"CoreFoundation", "CoreVideo", "CoreMedia", "AudioToolbox", "VideoToolbox", "Security"}

# Zhulong 自有静态归档（不经 FFmpeg .pc）在 Linux 下需要的系统库。
# 它们不属于 FFmpeg 的依赖，因此不能走 system_libraries() 的白名单审计，
# 但必须同样显式声明，避免「直接 append 一个 -l 标志」绕过审计边界。
ZHULONG_SYSTEM_LIBRARIES = {
    "linux": ["-lrt"],  # ZhulongAlgoIpc 的 POSIX 共享内存/信号量 (CMakeLists: UNIX AND NOT APPLE)
}


def system_libraries(prefix):
    """解析已安装 FFmpeg 目标产物 .pc 文件中声明的系统库，实施白名单安全审计"""
    libraries = []
    for lib in ("avformat", "avcodec", "avutil"):
        for line in (prefix / "lib/pkgconfig" / f"lib{lib}.pc").read_text().splitlines():
            if not line.startswith(("Libs:", "Libs.private:")):
                continue
            tokens = shlex.split(line.split(":", 1)[1])
            index = 0
            while index < len(tokens):
                flag = tokens[index]
                if flag.startswith("-L") or flag in ("-lavformat", "-lavcodec", "-lavutil"):
                    index += 1
                    continue
                if flag == "-framework":
                    if index + 1 >= len(tokens):
                        raise ValueError("dangling -framework in pkg-config")
                    framework = tokens[index + 1]
                    if framework not in ALLOWED_FRAMEWORKS:
                        raise ValueError(f"unexpected static FFmpeg framework {framework}; review target recipe")
                    combined = f"-framework {framework}"
                    if combined not in libraries:
                        libraries.append(combined)
                    index += 2
                    continue
                # 仅允许受控的基础系统库
                if flag not in ("-pthread", "-lm", "-latomic", "-ldl"):
                    raise ValueError(f"unexpected static FFmpeg dependency {flag}; review target recipe")
                if flag not in libraries:
                    libraries.append(flag)
                index += 1
    return libraries


def configure(p, sanitizer):
    """配置 CMake 构建环境（包含 Sanitizer 注入与交叉编译 Toolchain 生成）"""
    prefix = dependency(p)
    libraries = system_libraries(prefix)
    build = ROOT / "build/native" / (p["mode"] + ("-" + sanitizer if sanitizer else ""))
    build.mkdir(parents=True, exist_ok=True)

    args = [
        "cmake", "-S", ROOT / "native", "-B", build, "-DCMAKE_BUILD_TYPE=RelWithDebInfo",
        f"-DFFMPEG_ROOT={prefix}", f"-DFFMPEG_SYSTEM_LIBRARIES={';'.join(libraries)}",
        f"-DCMAKE_C_COMPILER={p['cc']}", f"-DCMAKE_CXX_COMPILER={p['cxx']}",
        f"-DCMAKE_AR={p['ar']}", f"-DCMAKE_RANLIB={p['ranlib']}",
        f"-DCMAKE_C_FLAGS={shlex.join(p['cflags'])}", f"-DCMAKE_CXX_FLAGS={shlex.join(p['cflags'])}",
        f"-DCMAKE_EXE_LINKER_FLAGS={shlex.join(p['ldflags'])}",
        f"-DZHULONG_SANITIZER={sanitizer or ''}", "-DBUILD_TESTING=ON",
        # 用 .get() 兼容直接调用 configure() 的单元测试（它们绕过 profile() 构造最小 profile）。
        f"-DZHULONG_INFERENCE_BACKEND={p.get('inference_backend', 'cpu')}",
        f"-DZHULONG_VISION_BACKEND={p.get('vision_backend', 'cpu')}"
    ]
    # 只有 rknn 才调 rknn_prefix()：cpu 构建不能因为缺 vendor 目录而失败。
    # 使用绝对文件路径链接，不把 RKNN 前缀塞进 CMAKE_FIND_ROOT_PATH。
    if p.get("inference_backend", "cpu") == "rknn":
        args.append(f"-DZHULONG_RKNN_ROOT={rknn_prefix()}")
    elif p.get("inference_backend", "cpu") == "onnx":
        args.append(f"-DZHULONG_ONNX_ROOT={onnx_prefix(p.get('onnx_root'))}")
    # 同理，只有 rga 才校验 RGA 前缀。
    if p.get("vision_backend", "cpu") == "rga":
        args.append(f"-DZHULONG_RGA_ROOT={rga_prefix()}")

    if p["mode"] != "host":
        def quote(value):
            return '"' + value.replace('\\', '/').replace('"', '\\"') + '"'
        toolchain = build / "toolchain.cmake"
        toolchain.write_text("\n".join([
            "set(CMAKE_SYSTEM_NAME Linux)", f"set(CMAKE_SYSTEM_PROCESSOR {quote(p['arch'])})",
            f"set(CMAKE_SYSROOT {quote(p['sysroot'])})",
            f"set(CMAKE_FIND_ROOT_PATH {quote(p['sysroot'])} {quote(str(prefix))})",
            "set(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)", "set(CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY)",
            "set(CMAKE_FIND_ROOT_PATH_MODE_INCLUDE ONLY)", "set(CMAKE_FIND_ROOT_PATH_MODE_PACKAGE ONLY)", ""
        ]))
        args += [f"-DCMAKE_TOOLCHAIN_FILE={toolchain}"]

    run(args)
    return build, prefix


def go(p, build, prefix, args):
    """执行 Go 构建命令（动态注入内容寻址的 CGO 链接参数）

    ⚠️ 解决 Go 构建缓存不感知外部 .a 变化的根本方法：
      为 libZhulongEngine.a、libZhulongAlgoIpc.a 与 libZhulongLog.a 分别计算内容哈希，复制到独立的哈希子目录下进行链接。
    """
    engine_archive = build / "libZhulongEngine.a"
    engine_link = build / "go-link" / digest(engine_archive) / engine_archive.name
    if not engine_link.exists():
        engine_link.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(engine_archive, engine_link)

    ipc_archive = build / "libZhulongAlgoIpc.a"
    ipc_link = build / "go-link" / digest(ipc_archive) / ipc_archive.name
    if not ipc_link.exists():
        ipc_link.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(ipc_archive, ipc_link)

    log_archive = build / "libZhulongLog.a"
    log_link = build / "go-link" / digest(log_archive) / log_archive.name
    if not log_link.exists():
        log_link.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(log_archive, log_link)

    cgo_sys_libs = []
    for flag in system_libraries(prefix):
        if flag.startswith("-framework "):
            cgo_sys_libs.extend(flag.split(" ", 1))
        else:
            cgo_sys_libs.append(flag)

    libraries = [engine_link, ipc_link, log_link] + [prefix / "lib" / f"lib{lib}.a" for lib in ("avformat", "avcodec", "avutil")]
    flags = list(map(str, libraries)) + [
        "-lc++" if p["target_os"] == "darwin" else "-lstdc++",
        *cgo_sys_libs, "-pthread"
    ]
    # Zhulong 自有归档的系统库依赖，按目标 OS 显式追加（见 ZHULONG_SYSTEM_LIBRARIES）。
    # ⚠️ 必须排在三个归档之后：GNU ld 对静态库与 -l 的顺序敏感。
    for lib in ZHULONG_SYSTEM_LIBRARIES.get(p["target_os"], []):
        if lib not in flags:
            flags.append(lib)

    env = os.environ.copy()
    env.update(CGO_ENABLED="1", GOOS=p["target_os"], GOARCH=p["goarch"], CC=p["cc"], CXX=p["cxx"])

    if p["mode"] != "host":
        flags += [f"--sysroot={p['sysroot']}", *p["ldflags"]]
        cflags = [f"--sysroot={p['sysroot']}", *p["cflags"]]
        env.update(CGO_CFLAGS=shlex.join(cflags), CGO_CXXFLAGS=shlex.join(cflags))
        if p.get("goarm"):
            env["GOARM"] = p["goarm"]

    env["CGO_LDFLAGS"] = shlex.join(flags)
    (build / "go-link.json").write_text(
        json.dumps({k: env[k] for k in ("CC", "CXX", "GOOS", "GOARCH", "CGO_LDFLAGS")}, indent=2) + "\n"
    )
    run([os.environ.get("GO", "go"), *args], cwd=ROOT, env=env)


def validate_cross_go(p, args):
    """严格校验交叉编译 Go 命令行参数，拒绝非法参数或在 Host 误执行目标二进制"""
    if not args or args[0] not in ("build", "test"):
        raise ValueError("cross Go commands must build (or test -c), never execute target tests")
    if "-args" in args or "--" in args:
        raise ValueError("cross Go builds do not accept target runtime arguments")

    compile_flags = [arg for arg in args[1:] if arg == "-c" or arg.startswith("-c=")]
    if args[0] == "test" and compile_flags not in (["-c"], ["-c=true"]):
        raise ValueError("cross Go tests require exactly one enabled -c flag")

    outputs = []
    for index, arg in enumerate(args[1:], 1):
        if arg == "-o":
            if index + 1 == len(args) or args[index + 1].startswith("-"):
                raise ValueError("cross Go -o requires an output path")
            outputs.append(args[index + 1])
        elif arg.startswith("-o="):
            outputs.append(arg[3:])

    if len(outputs) != 1 or not outputs[0]:
        raise ValueError("cross Go build requires exactly one -o build/targets/<profile>/...")

    destination = Path(outputs[0]).resolve()
    if not destination.is_relative_to(ROOT / "build/targets" / p["name"]):
        raise ValueError("cross output must be under build/targets/<profile>/ (host outputs protected)")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", help="显式交叉编译目标 JSON 配置；省略则表示编译为 Host 本地模式")
    parser.add_argument("--sanitizer", choices=("address", "thread"), help="开启 ASan 或 TSan 内存/线程检查")
    parser.add_argument("--inference-backend", choices=("cpu", "rknn", "onnx"), help="算法运行库推理后端")
    parser.add_argument("--onnx-root", help="ONNX Runtime 前缀路径（覆盖 ZHULONG_ONNX_ROOT）")
    sub = parser.add_subparsers(dest="command", required=True)

    prep = sub.add_parser("prepare", help="准备依赖源码压缩包")
    prep.add_argument("--archive", help="本地源码包路径")
    prep.add_argument("--download", action="store_true", help="允许联网下载固定版本的 FFmpeg 源码")

    for name in ("configure", "build", "test"):
        cmd_parser = sub.add_parser(name)
        cmd_parser.add_argument("--sanitizer", dest="sub_sanitizer", choices=("address", "thread"), help="开启 ASan 或 TSan 内存/线程检查")
        cmd_parser.add_argument("--inference-backend", dest="sub_inference_backend", choices=("cpu", "rknn", "onnx"), help="算法运行库推理后端")
        cmd_parser.add_argument("--onnx-root", dest="sub_onnx_root", help="ONNX Runtime 前缀路径（覆盖 ZHULONG_ONNX_ROOT）")

    sub.add_parser("go", help="调用 Go 编译器（带 CGO 隔离参数）").add_argument("args", nargs=argparse.REMAINDER)

    args = parser.parse_args()

    if args.command == "prepare":
        prepare(args.archive, args.download)
        return

    sanitizer = args.sanitizer or getattr(args, "sub_sanitizer", None)
    inference_backend = args.inference_backend or getattr(args, "sub_inference_backend", None)
    onnx_root = args.onnx_root or getattr(args, "sub_onnx_root", None)

    p = profile(args.profile)
    if inference_backend:
        p["inference_backend"] = inference_backend
    if onnx_root:
        p["onnx_root"] = onnx_root

    if p["mode"] != "host" and args.command == "test":
        raise ValueError("cross tests cannot run on the host; use build then inspect/deploy")

    if args.command == "go":
        if sanitizer:
            raise ValueError("sanitizer builds are Native-only; Go race is a separate check")
        if p["mode"] != "host":
            validate_cross_go(p, args.args)

    build, prefix = configure(p, sanitizer)

    if args.command != "configure":
        run(["cmake", "--build", build, "--parallel", str(min(os.cpu_count() or 2, 8))])

    if args.command == "test":
        run(["ctest", "--test-dir", build, "--output-on-failure"])

    if args.command == "go":
        go(p, build, prefix, args.args)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        sys.exit(f"native build failed: {error}")
