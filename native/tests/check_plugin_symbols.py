"""校验算法插件动态符号表：只允许导出白名单内的纯 C ABI 符号。

用法：
    check_plugin_symbols.py <nm> <plugin.so>[=sym1,sym2] ...

未显式指定期望符号时，默认只允许算法插件的统一入口 `zhulong_plugin_get_api`。
人脸识别插件额外导出宿主 dlopen 依赖的单次特征提取/释放接口。
"""

import subprocess
import sys


def parse_spec(spec):
    path, _, expected = spec.partition("=")
    symbols = expected.split(",") if expected else ["zhulong_plugin_get_api"]
    return path, symbols


def main():
    nm, *specs = sys.argv[1:]
    for spec in specs:
        plugin, expected = parse_spec(spec)
        result = subprocess.run([nm, "-D", "--defined-only", plugin], check=True, capture_output=True, text=True)
        symbols = sorted(line.split()[-1] for line in result.stdout.splitlines() if len(line.split()) >= 3)
        assert symbols == sorted(expected), (plugin, symbols, expected)


if __name__ == "__main__":
    main()
