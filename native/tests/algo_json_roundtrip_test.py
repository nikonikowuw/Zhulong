import json
import subprocess
import sys

expected = 'quote " slash \\ control \b\f\n\r\t' + chr(1) + " UTF-8: 中"
result = subprocess.run([sys.argv[1]], check=True, capture_output=True, text=True, encoding="utf-8")
assert json.loads(result.stdout) == expected
