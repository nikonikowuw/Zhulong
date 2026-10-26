#!/usr/bin/env python3
"""校验算法制品的动态符号表。

交付形态只有一个：包根可执行文件（`<manifest.id>`，静态链接运行时）。

    check_plugin_symbols.py --entry <package_executable>

断言：**不得导出任何插件 C ABI 符号**（`zhulong_*` / `zhulong_plugin_get_api`）。
残留导出意味着旧 dlopen 通道还活着——那正是本任务要消灭的东西。

口径说明：`nm -D --defined-only` 在任何 PIE 上都会列出约 15 条符号：弱符号化的
libstdc++ 模板实例（`_ZNSt16_Sp_counted_base…`）与 libc 的 COPY 重定位
（`stderr@GLIBC_2.2.5`）。把它们算进「导出表必须为空」只会逼人上 version script
去掩盖真问题，所以断言只针对插件契约面。
"""

import shutil
import subprocess
import sys

PLUGIN_ENTRY = "zhulong_plugin_get_api"


def defined_symbols(nm, path):
    result = subprocess.run([nm, "-D", "--defined-only", path], check=True, capture_output=True, text=True)
    return sorted(line.split()[-1] for line in result.stdout.splitlines() if len(line.split()) >= 3)


def check_entry(nm, path):
    leaked = [s for s in defined_symbols(nm, path) if s.startswith("zhulong_") or s == PLUGIN_ENTRY]
    assert not leaked, (path, "package executable still exports plugin C ABI", leaked)


def main(argv):
    if len(argv) != 2 or argv[0] != "--entry":
        sys.stderr.write("usage: check_plugin_symbols.py --entry <package_executable>\n")
        return 2
    check_entry(shutil.which("nm") or "nm", argv[1])
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
