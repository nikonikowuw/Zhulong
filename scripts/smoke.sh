#!/usr/bin/env bash
set -euo pipefail

binary="${1:-build/Zhulong}"
if [[ "$binary" != /* ]]; then
  binary="$PWD/$binary"
fi
if [[ ! -x "$binary" ]]; then
  printf 'Smoke-test binary is missing or not executable: %s\n' "$binary" >&2
  exit 1
fi

temporary_directory="$(mktemp -d)"
application_pid=""
cleanup() {
  if [[ -n "$application_pid" ]] && kill -0 "$application_pid" 2>/dev/null; then
    kill -TERM "$application_pid" 2>/dev/null || true
    wait "$application_pid" 2>/dev/null || true
  fi
  rm -rf "$temporary_directory"
}
trap cleanup EXIT

port="$(python3 -c 'import socket; listener = socket.socket(); listener.bind(("127.0.0.1", 0)); print(listener.getsockname()[1]); listener.close()')"
address="127.0.0.1:${port}"
printf '[http]\naddress = "%s"\n\n[data]\ndirectory = "./data"\n\n[logging]\ndevelopment = false\n' "$address" >"$temporary_directory/config.toml"
cd "$temporary_directory"
"$binary" >"$temporary_directory/application.log" 2>&1 &
application_pid=$!

health_file="$temporary_directory/health.json"
for attempt in $(seq 1 60); do
  if ! kill -0 "$application_pid" 2>/dev/null; then
    cat "$temporary_directory/application.log" >&2
    printf 'Zhulong exited before becoming ready.\n' >&2
    exit 1
  fi
  if curl --silent --fail "http://${address}/api/v1/health" >"$health_file"; then
    break
  fi
  sleep 0.1
done
if [[ ! -s "$health_file" ]]; then
  cat "$temporary_directory/application.log" >&2
  printf 'Health endpoint did not become ready.\n' >&2
  exit 1
fi
python3 -c 'import json,sys; body=json.load(open(sys.argv[1], encoding="utf-8")); assert set(body)=={"code","message","data"}, body; assert body["code"]=="OK", body; assert body["data"]["status"]=="ready", body' "$health_file"

root_file="$temporary_directory/root.html"
curl --silent --show-error --fail "http://${address}/" >"$root_file"
grep -qi 'Zhulong' "$root_file"
curl --silent --show-error --fail "http://${address}/favicon.svg" >"$temporary_directory/favicon.svg"
grep -q '<svg' "$temporary_directory/favicon.svg"
client_route_file="$temporary_directory/client-route.html"
curl --silent --show-error --fail "http://${address}/settings/network" >"$client_route_file"
cmp -s "$root_file" "$client_route_file"

api_status="$(curl --silent --show-error --output "$temporary_directory/api-error.json" --write-out '%{http_code}' "http://${address}/api/v1/missing")"
if [[ "$api_status" != "404" ]]; then
  printf 'Expected unmatched API route to return 404, got %s.\n' "$api_status" >&2
  exit 1
fi
python3 -c 'import json,sys; body=json.load(open(sys.argv[1], encoding="utf-8")); assert set(body)=={"code","message","data"}, body; assert body["code"]=="ROUTE_NOT_FOUND" and body["data"] is None, body' "$temporary_directory/api-error.json"

curl --silent --show-error --fail "http://${address}/swagger/doc.json" >"$temporary_directory/swagger.json"
python3 -c 'import json,sys; body=json.load(open(sys.argv[1], encoding="utf-8")); response=body["definitions"]["httputil.Response"]; assert body["swagger"]=="2.0", body; assert set(response["required"])=={"code","message","data"}, response' "$temporary_directory/swagger.json"

kill -TERM "$application_pid"
wait "$application_pid"
application_pid=""
printf 'Smoke test passed: health, embedded SPA, API 404 isolation, Swagger, graceful stop.\n'
