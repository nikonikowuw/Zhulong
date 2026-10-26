#!/usr/bin/env python3
"""校验算法制品的动态符号表。

两种形态，两种断言：

1. 旧形态（`.so` 插件，dlopen 装载）——只允许导出白名单内的纯 C ABI 符号：
       check_plugin_symbols.py <nm> <plugin.so>[=sym1,sym2] ...
   未显式指定期望符号时，默认只允许算法插件的统一入口 `zhulong_plugin_get_api`。

2. 交付形态（包根可执行文件）——绝不允许残留任何插件 C ABI 导出：
       check_plugin_symbols.py --entry <package_executable>
   「导出表为空」的精确口径是「没有 zhulong_* 导出、也没有 zhulong_plugin_get_api」：
   弱符号化的 libstdc++ 模板实例与 libc 的 COPY 重定位无法消除，也不构成插件契约面，
   把它们算进来只会让断言变成噪声（或被迫用 version script 掩盖真问题）。
"""

import shutil
import subprocess
import sys

PLUGIN_ENTRY = "zhulong_plugin_get_api"


def parse_spec(spec):
    path, _, expected = spec.partition("=")
    symbols = expected.split(",") if expected else [PLUGIN_ENTRY]
    return path, symbols


def defined_symbols(nm, path):
    result = subprocess.run([nm, "-D", "--defined-only", path], check=True, capture_output=True, text=True)
    return sorted(line.split()[-1] for line in result.stdout.splitlines() if len(line.split()) >= 3)


def check_plugin(nm, spec):
    plugin, expected = parse_spec(spec)
    symbols = defined_symbols(nm, plugin)
    assert symbols == sorted(expected), (plugin, symbols, expected)


def check_entry(nm, path):
    """包根可执行文件不得导出插件 C ABI：多了就说明旧形态的入口还留在里面。"""
    leaked = [s for s in defined_symbols(nm, path) if s.startswith("zhulong_") or s == PLUGIN_ENTRY]
    assert not leaked, (path, "package executable still exports plugin C ABI", leaked)


def main(argv):
    if len(argv) == 2 and argv[0] == "--entry":
        check_entry(shutil.which("nm") or "nm", argv[1])
        return 0
    nm, *specs = argv
    for spec in specs:
        check_plugin(nm, spec)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
