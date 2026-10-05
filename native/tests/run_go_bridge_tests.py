#!/usr/bin/env python3
"""Go/CGO 媒体桥接与生命周期真实 RTSP 集成测试运行器

职责与规范：
1. 启动本地真实 RTSP 服务桩 (rtsp_fixture.Server) 并分配动态端口。
2. 注入带合成凭据的地址 (ZHULONG_RTSP_TEST_SERVER=rtsp://fixture-user:fixture-password@127.0.0.1:<port>)。
3. 调用 python3 native/scripts/build.py go test 运行真实桥接集成测试 (TestBridgeIntegration)。
4. 捕获 stdout 与 stderr，严格校验无敏感凭据泄露 (fixture-user / fixture-password)。
5. 校验物理连接计数与生命周期清理，保证测试结束后零残留连接。
"""
import collections
import os
from pathlib import Path
import subprocess
import sys
import threading
import time

CURRENT_DIR = Path(__file__).resolve().parent
ROOT_DIR = CURRENT_DIR.parent.parent
sys.path.insert(0, str(CURRENT_DIR))

from rtsp_fixture import Server


def main():
    server = Server()
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    port = server.server_address[1]
    test_url = f"rtsp://fixture-user:fixture-password@127.0.0.1:{port}"

    env = dict(os.environ)
    env["ZHULONG_RTSP_TEST_SERVER"] = test_url

    cmd = [
        sys.executable,
        str(ROOT_DIR / "native/scripts/build.py"),
        "go",
        "test",
        "-race",
        "-count=1",
        "-v",
        "-run",
        "TestBridgeIntegration",
        "./internal/engine"
    ]

    print(f"Starting Go bridge integration test runner on {test_url}...")
    try:
        result = subprocess.run(cmd, cwd=ROOT_DIR, env=env, text=True, capture_output=True, timeout=120)
        sys.stdout.write(result.stdout)
        sys.stderr.write(result.stderr)
        captured = result.stdout + result.stderr

        # 核心安全断言：验证敏感凭据绝对未泄露到输出中
        if "fixture-user" in captured or "fixture-password" in captured:
            raise AssertionError("credential leak: sensitive RTSP credentials found in Go bridge output")

        if result.returncode != 0:
            print(f"Go bridge integration tests failed with exit code {result.returncode}", file=sys.stderr)
            return result.returncode

        time.sleep(0.3)
        with server.lock:
            print("RTSP fixture connections:", dict(server.connections))
            print("RTSP fixture transports:", dict(server.transports))
            print("RTSP fixture active connections:", dict(server.active))
            assert not any(server.active.values()), f"socket left open after Go engine tests: {dict(server.active)}"
            assert server.transports["tcp"] > 0, "expected TCP transport to be used"
            assert server.transports["udp"] > 0, "expected UDP transport to be used"

        print("Go bridge integration tests passed successfully!")
        return 0

    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


if __name__ == "__main__":
    sys.exit(main())
