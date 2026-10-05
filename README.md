# Zhulong

Zhulong is a local-first edge operations host: a Go HTTP host, an embedded React status console, SQLite migrations, and a C++17 native engine behind a C ABI. The native component supports compressed H.264/H.265 RTSP probe/capture with statically linked FFmpeg; Go currently exposes lifecycle calls only.

Camera CRUD/Go subscriptions, ONVIF, recording, continuous decoding, inference, accelerator SDKs, and target-board runtime support are not implemented or claimed.

## Prerequisites

- Go 1.27.1 or newer
- Node.js 24 and npm
- CMake 3.20 or newer
- A C and C++17 compiler
- Python 3.12 or newer for native dependency preparation/tests, and `curl` for the smoke test

The earlier host skeleton was verified on macOS arm64 with Go 1.27.1, Node 24.15.0, npm 11.12.1, CMake 4.4.0, and Apple Clang 17.0.0. The new FFmpeg ingestion path is verified on Linux x86_64 with GCC 14.2.0 (not a minimum version), CMake 3.31.6 and Go 1.27.1. Darwin linking is retained but this change is not macOS-validated. Explicit cross-build profiles are supported; actual target linking and RK3568 compatibility await a matching toolchain/sysroot and board evidence. See [native build, ABI and validation notes](native/README.md).

## Build and Run

Prepare the pinned FFmpeg source explicitly once (network); normal native builds are offline. A local archive can also be imported using the native build script:

```bash
make native-deps
```

Run all frontend, native, and Go checks from the repository root:

```bash
make check
```

Build the single Go host binary. The target first builds the frontend, then the C++ static library and Swagger 2.0 docs, and finally links them with CGO:

```bash
make build
```

Run the built-in integration smoke test or start the host:

```bash
make smoke
make run
```

The host reads `config.toml` from its working directory at startup; `make run` uses the repository's root config file. There are no startup flags: edit the TOML file and restart the service. The configuration is strict, so missing files, unknown keys, and invalid values stop startup. Omitted values use built-in defaults.

Start the host with:

```bash
./build/Zhulong
```

The default listener is `127.0.0.1:8080`. If `data.directory` is omitted, SQLite data is stored under the operating system's user config directory; otherwise `zhulong.db` is stored under the selected directory. Migrations run before the native engine starts and before HTTP binds. A migration failure stops startup.

The console is served from the Go binary at `/`. Health is available at `/api/v1/health`; generated API docs are at `/swagger/index.html`. Unmatched `/api/*` paths return the JSON error envelope and never fall through to the SPA.

For frontend work, run `make run` in one terminal and `make web-dev` in another. Vite proxies `/api` to `127.0.0.1:8080`.

## Native Boundary

`native/include/Zhulong/engine.h` is the sole public native header. C++ owns handles, worker threads, FFmpeg contexts and packet buffers; the existing Go wrapper only owns the engine handle value and serializes lifecycle calls. No Go pointer is passed to C, and all exported ABI functions isolate C++ exceptions. Native subscriptions borrow packets synchronously; unsubscribe and stop drain in-flight callbacks before returning. The Go host does not yet acquire streams or wrap subscriptions.

Use `make` or `python3 native/scripts/build.py go ...` for Go commands depending on `internal/engine`. The wrapper supplies explicit static FFmpeg libraries and a content-addressed Engine archive so Go's external-archive cache cannot hide native changes. See [native/README.md](native/README.md) for cross profiles, pure-C ownership, timeout limits, tests and LGPL distribution obligations.

The backend uses GORM with embedded, versioned `golang-migrate` SQL migrations. GORM `AutoMigrate` is intentionally not used. The UI health panel displays only values returned by the host health endpoint and includes loading, error/retry, and ready states.
