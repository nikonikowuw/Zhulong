# Zhulong

Zhulong is a local-first edge operations host. This repository currently contains an engineering foundation only: a Go HTTP host, an embedded React status console, SQLite migrations, and a hardware-independent C++ lifecycle stub behind a C ABI.

No camera ingestion, RTSP/ONVIF, recording, decoding, inference, accelerator SDK, or target-board support is implemented or claimed.

## Prerequisites

- Go 1.27.1 or newer
- Node.js 24 and npm
- CMake 3.20 or newer
- A C and C++17 compiler
- Python 3 and `curl` for the smoke test

The current verification baseline is macOS arm64 with Go 1.27.1, Node 24.15.0, npm 11.12.1, CMake 4.4.0, and Apple Clang 17.0.0. Linux host builds use the local C/C++ toolchain and `libstdc++`; no cross-compilation or board compatibility is implied.

## Build and Run

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

The default listener is `127.0.0.1:8080`. If `data.directory` is omitted, SQLite data is stored under the operating system's user config directory; otherwise `zhulong.db` is stored under the selected directory. Migrations run before the native stub starts and before HTTP binds. A migration failure stops startup.

The console is served from the Go binary at `/`. Health is available at `/api/v1/health`; generated API docs are at `/swagger/index.html`. Unmatched `/api/*` paths return the JSON error envelope and never fall through to the SPA.

For frontend work, run `make run` in one terminal and `make web-dev` in another. Vite proxies `/api` to `127.0.0.1:8080`.

## Native Boundary

`native/include/Zhulong/engine.h` is the sole public native header. C++ allocates and frees its opaque engine handle; the Go wrapper only owns the handle value and serializes lifecycle calls. No Go pointer is passed to C, and every exported ABI function catches C++ exceptions. The stub creates no worker threads and processes no media.

The backend uses GORM with embedded, versioned `golang-migrate` SQL migrations. GORM `AutoMigrate` is intentionally not used. The UI health panel displays only values returned by the host health endpoint and includes loading, error/retry, and ready states.
