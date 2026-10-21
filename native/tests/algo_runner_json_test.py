import json
import subprocess
import sys

runner, plugin = sys.argv[1:3]
missing_plugin = 'missing "quoted" \\ path\nplugin.so'
failed = subprocess.run(
    [runner, "--verify-plugin", missing_plugin],
    check=False,
    capture_output=True,
    text=True,
    encoding="utf-8",
)
assert failed.returncode != 0
start = json.loads(failed.stdout.splitlines()[0])
errors = [json.loads(line) for line in failed.stderr.splitlines()]
error = next(report for report in errors if report.get("status") == "failed")
native_log = next(report for report in errors if report.get("zhulongLog") is True)
assert start["plugin"] == missing_plugin
assert error["status"] == "failed"
assert native_log["module"] == "algorithm.runner"
assert native_log["message"] == "plugin_load_failed reason=dlopen"
assert missing_plugin not in json.dumps(native_log)

sample = 'sample "quoted" \\ path\ncontrol ' + chr(1)
passed = subprocess.run(
    [runner, "--verify-plugin", plugin, "--sample", sample],
    check=True,
    capture_output=True,
    text=True,
    encoding="utf-8",
)
reports = [json.loads(line) for line in passed.stdout.splitlines()]
assert [report["step"] for report in reports[:2]] == ["start", "abi_verified"]
assert reports[-1]["status"] == "passed"
assert reports[-1]["sample"] == sample
