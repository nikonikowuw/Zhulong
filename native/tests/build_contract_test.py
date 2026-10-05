#!/usr/bin/env python3
"""离线构建契约与交叉编译约束单元测试

测试目标：
  1. 离线安全性：验证下载依赖仅在显式命令下允许，校验 SHA-256 防篡改；
  2. 缓存隔离与内容寻址：验证 SDK / sysroot 变更、Engine 静态库变动能准确反映到缓存键与链接标识中；
  3. 环境隔离纯洁性：验证构建系统严格拒绝 CPATH、C_INCLUDE_PATH、LIBRARY_PATH 等外部隐式环境变量注入；
  4. 交叉编译安全防线：验证交叉编译参数校验（只允许编译 -c，禁止在 Host 误执行目标二进制，禁止 Host/Cross 库混用）；
  5. 依赖白名单审计：验证解析静态 .pc 依赖时，拒绝未审计的系统库引入。
"""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("native_build", ROOT / "native/scripts/build.py")
build = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build)
PREFIX = Path(sys.argv.pop(1)) if len(sys.argv) > 1 else None


class BuildContractTests(unittest.TestCase):
    def test_prepare_is_offline_and_checksum_checked(self):
        """测试 FFmpeg 源码解包严格离线校验且强校验 SHA256"""
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / "source.tar.xz"
            with patch.object(build, "ARCHIVE", archive), patch.object(build.urllib.request, "urlretrieve") as fetch:
                with self.assertRaisesRegex(ValueError, "verified FFmpeg source missing"):
                    build.prepare(None, False)
                archive.write_bytes(b"not the pinned release")
                with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                    build.prepare(archive, False)
                fetch.assert_not_called()

    def test_sdk_replacement_changes_identity(self):
        """测试 SDK 目录树内容变动会触发树哈希改变（而非仅依赖路径名或 mtime）"""
        with tempfile.TemporaryDirectory() as directory:
            library = Path(directory) / "libc.so"
            library.write_bytes(b"old")
            before = build.tree_digest(directory)
            library.write_bytes(b"new")
            self.assertNotEqual(before, build.tree_digest(directory))

    def test_sysroot_cannot_follow_external_host_links(self):
        """测试 sysroot 内部软链接若逃逸出目标树根目录则必须报错拒绝"""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sysroot = root / "sysroot"
            sysroot.mkdir()
            external = root / "host-lib"
            external.mkdir()
            (sysroot / "lib").symlink_to(external, target_is_directory=True)
            with self.assertRaisesRegex(ValueError, "symlink escapes target tree"):
                build.tree_digest(sysroot)

    def test_cross_profile_rejects_host_compiler(self):
        """测试交叉编译配置中若编译器 Target Triple 与目标架构不符时拒绝构建"""
        with tempfile.TemporaryDirectory() as directory:
            profile = dict(name="test", cc="cc", cxx="c++", ar="ar", ranlib="ranlib", sysroot=directory,
                           arch="aarch64", goarch="arm64", target_os="linux")
            if build.platform.machine() in ("aarch64", "arm64"):
                profile.update(arch="x86_64", goarch="amd64")
            path = Path(directory) / "profile.json"
            path.write_text(json.dumps(profile))
            with self.assertRaisesRegex(ValueError, "compiler target does not match"):
                build.profile(path)

    def test_ambient_compiler_search_paths_cannot_bypass_target_identity(self):
        """测试外部环境中残留的 CPATH/LIBRARY_PATH 等隐式变量会被主动拦截报错"""
        for variable in ("CPATH", "C_INCLUDE_PATH", "CPLUS_INCLUDE_PATH", "OBJC_INCLUDE_PATH", "LIBRARY_PATH",
                         "COMPILER_PATH", "GCC_EXEC_PREFIX", "SDKROOT", "MACOSX_DEPLOYMENT_TARGET", "CGO_CPPFLAGS", "CGO_CFLAGS",
                         "CGO_CXXFLAGS", "CGO_LDFLAGS"):
            with self.subTest(variable=variable), patch.dict(os.environ, {variable: "/untracked/host/input"}):
                with self.assertRaisesRegex(ValueError, variable):
                    build.profile(None)

    def test_cross_go_cannot_override_checked_output_or_execute_tests(self):
        """测试交叉编译 Go 命令拒绝在宿主机直接执行目标测试或覆盖主产物路径"""
        safe = str(ROOT / "build/targets/review-test/program")
        unsafe = str(ROOT / "build/Zhulong")
        for arguments in (["build", "-o", safe, "-o", unsafe],
                          ["build", "-o", safe, "-o=" + unsafe],
                          ["test", "-c", "-c=false", "-o", safe]):
            with self.subTest(arguments=arguments), patch.object(sys, "argv", ["build.py", "--profile", "unused", "go", *arguments]), \
                 patch.object(build, "profile", return_value={"mode": "cross-review-test", "name": "review-test"}), \
                 patch.object(build, "configure", side_effect=AssertionError("unsafe arguments reached configure")):
                with self.assertRaises(ValueError):
                    build.main()

    def test_cross_go_accepts_unambiguous_compile_only_outputs(self):
        """测试交叉编译只接受明确指向 build/targets/<profile>/ 的单目标编译命令"""
        safe = str(ROOT / "build/targets/review-test/program")
        profile = {"name": "review-test"}
        for arguments in (["build", "-o", safe, "./cmd/Zhulong"],
                          ["build", "-o=" + safe, "./cmd/Zhulong"],
                          ["test", "-c", "-o", safe, "./internal/engine"],
                          ["test", "-c=true", "-o=" + safe, "./internal/engine"]):
            with self.subTest(arguments=arguments):
                build.validate_cross_go(profile, arguments)
        for arguments in (["test", "-o", safe], ["test", "-c=false", "-o", safe],
                          ["build", "-o", str(ROOT / "build/Zhulong")], ["build", "-o"],
                          ["build", "-o="], ["run", "./cmd/Zhulong"]):
            with self.subTest(arguments=arguments), self.assertRaises(ValueError):
                build.validate_cross_go(profile, arguments)

    def make_pc(self, prefix, extra="-pthread -lm -latomic"):
        """构造模拟 pkg-config .pc 文件的测试辅助函数"""
        for name in ("avformat", "avcodec", "avutil"):
            pc = prefix / "lib/pkgconfig" / f"lib{name}.pc"
            pc.parent.mkdir(parents=True, exist_ok=True)
            pc.write_text(f"Libs: -L${{libdir}} -l{name} {extra}\nLibs.private:\n")

    def test_target_static_libraries_and_unknown_dependency(self):
        """测试静态系统库解析与非法外部依赖白名单拦截"""
        with tempfile.TemporaryDirectory() as directory:
            prefix = Path(directory)
            self.make_pc(prefix)
            self.assertEqual(build.system_libraries(prefix), ["-pthread", "-lm", "-latomic"])
            self.make_pc(prefix, "-lUnreviewedSharedCodec")
            with self.assertRaisesRegex(ValueError, "unexpected static FFmpeg dependency"):
                build.system_libraries(prefix)

    def test_engine_archive_content_invalidates_cgo_link_flags(self):
        """测试 Engine 静态归档内容变更会生成新的 CGO 链接路径（防止 Go CGO 缓存失效失灵）"""
        with tempfile.TemporaryDirectory() as directory:
            native = Path(directory) / "native"
            native.mkdir()
            prefix = Path(directory) / "ffmpeg"
            self.make_pc(prefix)
            archive = native / "libZhulongEngine.a"
            profile = dict(mode="host", target_os="linux", goarch="amd64", cc="cc", cxx="c++")
            with patch.object(build, "run") as run:
                archive.write_bytes(b"archive version one")
                build.go(profile, native, prefix, ["build", "./cmd/Zhulong"])
                first = run.call_args.kwargs["env"]["CGO_LDFLAGS"]
                archive.write_bytes(b"archive version two")
                build.go(profile, native, prefix, ["build", "./cmd/Zhulong"])
                second = run.call_args.kwargs["env"]["CGO_LDFLAGS"]
            self.assertNotEqual(first, second)
            self.assertIn("-latomic", second)
            self.assertEqual(len(list((native / "go-link").glob("*/libZhulongEngine.a"))), 2)

    def test_cross_flags_are_explicit_not_stale_cmake_initializers(self):
        """测试交叉编译标志通过 CMake Toolchain 显式注入而非继承残留配置"""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            prefix = root / "ffmpeg"
            self.make_pc(prefix)
            profile = dict(mode="cross-test", arch="aarch64", sysroot=str(root / "sysroot"),
                           cc="target-gcc", cxx="target-g++", ar="target-ar", ranlib="target-ranlib",
                           cflags=["-mabi=lp64"], ldflags=["-Wl,--as-needed"])
            with patch.object(build, "ROOT", root), patch.object(build, "dependency", return_value=prefix), patch.object(build, "run") as run:
                native, _ = build.configure(profile, None)
            command = run.call_args.args[0]
            self.assertIn("-DCMAKE_C_FLAGS=-mabi=lp64", command)
            self.assertIn("-DCMAKE_CXX_FLAGS=-mabi=lp64", command)
            self.assertIn("-DCMAKE_EXE_LINKER_FLAGS=-Wl,--as-needed", command)
            self.assertIn("-DCMAKE_AR=target-ar", command)
            self.assertIn("-DCMAKE_RANLIB=target-ranlib", command)
            toolchain = (native / "toolchain.cmake").read_text()
            self.assertIn("CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY", toolchain)
            self.assertIn(str(root / "sysroot"), toolchain)

    def test_cmake_missing_dependency_fails_without_download(self):
        """测试在缺少 FFmpeg 依赖时 CMake 明确报错退出，绝不在 configure 阶段尝试静默联网"""
        with tempfile.TemporaryDirectory() as directory:
            result = subprocess.run(["cmake", "-S", str(ROOT / "native"), "-B", directory],
                                    text=True, capture_output=True, timeout=30)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Missing verified static FFmpeg prefix", result.stdout + result.stderr)

    @unittest.skipUnless(PREFIX, "requires current verified FFmpeg prefix")
    def test_cross_cmake_rejects_real_host_ffmpeg(self):
        """测试交叉编译配置下 CMake 拒绝复用 Host 模式编译出的 FFmpeg 依赖"""
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            toolchain = path / "toolchain.cmake"
            toolchain.write_text("set(CMAKE_SYSTEM_NAME Linux)\nset(CMAKE_SYSTEM_PROCESSOR aarch64)\n")
            manifest = json.loads((PREFIX / "zhulong-ffmpeg.json").read_text())
            profile = manifest["profile"]
            result = subprocess.run(["cmake", "-S", str(ROOT / "native"), "-B", str(path / "build"),
                f"-DCMAKE_TOOLCHAIN_FILE={toolchain}", f"-DFFMPEG_ROOT={PREFIX}",
                f"-DCMAKE_C_COMPILER={profile['cc']}", f"-DCMAKE_CXX_COMPILER={profile['cxx']}"],
                text=True, capture_output=True, timeout=30)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Cross Engine cannot use host FFmpeg", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
