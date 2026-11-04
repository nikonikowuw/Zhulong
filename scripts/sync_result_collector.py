#!/usr/bin/env python3
"""一次性 HTTP 收集器：把浏览器里测量用例 POST 上来的 JSON 落盘。

为什么需要它：Vitest 的 `silent: 'passed-only'` 会吞掉**通过**用例的 console 输出，
而浏览器模式下 `onConsoleLog` 钩子不生效。测量类用例的价值恰恰在于「通过了，数字是多少」，
所以让被测页面直接把结果 POST 出来，比为了看一行数字故意让用例失败可靠。

只接受一个 POST，写盘后退出；同时把结果回显到 stdout 便于盯日志。

用法：
  scripts/sync_result_collector.py --port 18082 --out /tmp/sync-result.json &
  # 测量侧：VITE_SYNC_RESULT_URL=http://127.0.0.1:18082/sync
"""

import argparse
import http.server
import json
import pathlib
import sys


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=18082)
    parser.add_argument("--out", default="/tmp/sync-result.json")
    args = parser.parse_args()

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_POST(self):  # noqa: N802 - BaseHTTPRequestHandler 的约定
            length = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(length)
            pathlib.Path(args.out).write_bytes(body)
            try:
                print(json.dumps(json.loads(body), ensure_ascii=False, indent=2), flush=True)
            except ValueError:
                print(body.decode(errors="replace"), flush=True)
            # 浏览器以 mode:'no-cors' 发送，响应内容不可读；200 只是让请求干净结束。
            self.send_response(200)
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()
            self.wfile.write(b"ok")

        def do_OPTIONS(self):  # noqa: N802
            self.send_response(204)
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Access-Control-Allow-Methods", "POST, OPTIONS")
            self.send_header("Access-Control-Allow-Headers", "Content-Type")
            self.end_headers()

        def log_message(self, *_args):
            pass

    server = http.server.HTTPServer(("127.0.0.1", args.port), Handler)
    print(f"collector listening on http://127.0.0.1:{args.port}/ → {args.out}", flush=True)
    try:
        server.handle_request()
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
