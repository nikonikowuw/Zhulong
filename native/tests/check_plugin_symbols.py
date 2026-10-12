import subprocess
import sys

nm, *plugins = sys.argv[1:]
for plugin in plugins:
    result = subprocess.run([nm, "-D", "--defined-only", plugin], check=True, capture_output=True, text=True)
    symbols = [line.split()[-1] for line in result.stdout.splitlines() if len(line.split()) >= 3]
    assert symbols == ["zhulong_plugin_get_api"], (plugin, symbols)
