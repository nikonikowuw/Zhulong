# Native RTSP ingestion

This component is C++17 + pinned static FFmpeg behind the sole public C header,
`include/Zhulong/engine.h`. It probes and captures compressed H.264/H.265 packets.
It does **not** implement a decoding pipeline, Go camera/subscription API, recording,
preview transport, MPP/RGA/RKNN, or hardware acceleration. The Go host still uses
only engine lifecycle calls; health readiness does not imply a connected camera.

## Host builds

Requirements: CMake >=3.20, a C compiler supporting FFmpeg's C11 requirements,
a C++17 compiler/standard library with threads, Make, Python >=3.12 (safe tar
extraction), and the existing Go/frontend prerequisites. There is no GCC version
floor. This change was tested on Linux x86_64 with GCC 14.2.0, CMake 3.31.6,
Python 3.13.5 and Go 1.27.1. Darwin's libc++ link path is retained but untested.

```sh
# Explicit network operation, once. HTTPS archive + pinned SHA-256 verification.
make native-deps
# Or supply an already downloaded archive (no network):
python3 native/scripts/build.py prepare --archive /path/to/ffmpeg-7.1.5.tar.xz

# All native configure/build/test commands below are offline.
make native-build
make native-test
make go-check
python3 native/scripts/build.py go build -o build/Zhulong ./cmd/Zhulong

# Existing full-stack checks/build (frontend npm targets retain their own behavior):
make check
make smoke
```

Do not run bare `go build/test/vet` for packages that depend on `internal/engine`:
the wrapper supplies the selected static archives and platform runtime libraries.
Make and Air's build command use it. Air's existing watcher watches Go/TOML only;
run `make native-build`/the wrapper after native source changes rather than assuming
a C++ edit itself triggers Air.

Paths:

- Verified source: `build/deps/sources/ffmpeg-7.1.5.tar.xz`.
- Dependency cache: `build/deps/ffmpeg/<host|cross-NAME>/<identity>/` with source,
  `objects/`, `build.log`, and `install/`.
- Native: `build/native/host/` or `build/native/cross-NAME/`; sanitizer directories
  are separate (`host-address`, `host-thread`).
- `install/zhulong-ffmpeg.json` records source/configuration/toolchain identity.
  Cache identity includes source digest, recipe/resolver, compiler binaries,
  versions/backends/runtime inputs, flags, target, and sysroot content digest.
- FFmpeg's three target `.pc` files supply system libraries (including `-latomic`
  where configured), without searching a host pkg-config database. Unexpected
  external dependencies are rejected. A deliberately empty package resolver
  disables all external FFmpeg package detection.
- `build/native/host/go-link/<archive-sha256>/libZhulongEngine.a` is an immutable
  link snapshot; `go-link.json` records the last Go link environment. Go does not
  hash external archives, so changing an archive at a fixed `-L` path is unsafe.
  Content-addressing changes CGO flags and invalidates the correct cache entry.

CMake validates compiler identities and host/cross/sysroot consistency, requires
explicit archives, and compile/links a real C++17/thread/FFmpeg capability test.
It never downloads or falls back to shared FFmpeg. Cache/source failures are
explicit. Engine edits do not rebuild an unchanged FFmpeg dependency identity.

## Cross build (mechanism implemented, target validation pending)

The RK3568's installed Debian userspace architecture/version is **unknown**.
`profiles/linux-arm64.example.json` is a template, **not a board release profile**.
Supply an approved C/C++ target toolchain and matching sysroot on the development
host, then copy/edit the template. For a 32-bit ARM userspace use `arch: "arm"`,
`goarch: "arm"`, and explicit `goarm: "5"`, `"6"`, or `"7"`; select matching
compiler ABI flags in `cflags`/`ldflags`. Do not infer userspace from the SoC name.

```sh
make native-cross-build CROSS_PROFILE=/path/to/target.json \
  CROSS_OUTPUT=build/targets/PROFILE_NAME/Zhulong
# Compile Native test programs, but do not execute them on the host:
python3 native/scripts/build.py --profile /path/to/target.json build
# Go tests can be compiled, not run, with the cross wrapper:
python3 native/scripts/build.py --profile /path/to/target.json go test -c \
  -o build/targets/PROFILE_NAME/engine.test ./internal/engine
```

Profiles require `name`, `cc`, `cxx`, `ar`, `ranlib`, `sysroot`, `arch`, `goarch`,
`target_os` (currently Linux cross targets). `cflags`/`ldflags` are argument lists;
ambient compiler/CGO flags, compiler search paths (such as CPATH, C_INCLUDE_PATH,
CPLUS_INCLUDE_PATH, LIBRARY_PATH, COMPILER_PATH and GCC_EXEC_PREFIX), SDKROOT and
MACOSX_DEPLOYMENT_TARGET are rejected before tool discovery. They can bypass
sysroot search rules or silently change cached inputs. The sysroot must be
self-contained: symlinks escaping its tree are rejected instead of following host
headers/libraries (materialize or repair an SDK copy explicitly if needed). Toolchain, sysroot, FFmpeg,
CMake and Go target selection share one profile; CMake receives the selected
archiver and ranlib explicitly as well as the C/C++ compilers. Cross output must be beneath
`build/targets/<name>/`; exactly one `-o` or `-o=...` is allowed. Cross test
compilation requires exactly one `-c` or `-c=true`, with no runtime arguments or
later flag that disables compile-only mode. A cross `test` execution is rejected. Missing SDK inputs
fail rather than downloading a toolchain or reusing a host archive.

Before deployment, inspect the final executable and archive members using the
**target** `readelf`/`ar`: ELF machine, interpreter, `NEEDED`, and version-info.
Static FFmpeg does not make libc/libstdc++ static or compatible with older images.
Our Linux host binary requires symbols up to GLIBC_2.38 and GLIBCXX_3.4.30; these
are observations, not target requirements or supported-board claims. The current
environment has no ARM compiler or target sysroot; actual cross linking, target
ELF checks, and board smoke tests remain blocked, not passed.

## ABI ownership and threading

See `engine.h` for exact signatures, structs, units and status constants.

- `engine_create/start/stop/destroy` retain their old symbols/status values.
  Start/stop are idempotent; stop cancels registered probes and capture workers,
  joins the reaper/workers and drains callbacks. Restart begins with an empty pool.
- `engine_probe` uses an independent synchronous RTSP connection. Stop cancels
  registered probes. Failure clears the result handle. `probe_result_view` borrows
  Native-owned metadata/extradata until `probe_result_destroy`; results remain
  usable independently of engine lifetime. Extradata is unbounded by a fixed ABI
  array, never truncated, and may be empty. It preserves FFmpeg's representation.
- `stream_acquire` copies a valid escaped RTSP URI and deduplicates scheme/host/
  default port/outer whitespace only. Path, query and credentials retain semantics.
  Ambiguous multiple `@` in authority, bare whitespace, bad escapes, fragments,
  absent hosts and invalid ports are rejected. DNS, IPv6 and authentication beyond
  the loopback fixture are not certified by these tests.
- Consumers are nonzero IDs scoped to a stream. Duplicate acquire returns
  `DUPLICATE` without incrementing references. Conflicting transport/timeouts for
  one URL return `CONFIG_CONFLICT`. TCP is default; UDP is explicit, not fallback.
- `stream_subscribe` requires an existing Preview consumer, one subscription per
  consumer, and does not add a consumer reference. No subscription means no packet
  callback. Recording/AI references do not imply recording/inference implementation.
- `stream_unsubscribe` removes registration, prevents new callback admission, then
  drains a possible in-flight callback before returning. Release with an attached subscription is
  `BUSY`; use **unsubscribe → release**. After the last release, an 8-second steady
  clock grace period starts; reacquire before expiry preserves that stream.
- Capture errors are polled via `stream_get_status` as controlled status/error
  codes, never raw FFmpeg messages. There is no automatic reconnect. A failed
  stream remains failed while referenced or in grace; reacquire during grace does
  not restart it. To retry independently, release all consumers and allow expiry,
  or stop/start the whole engine. Later business-layer retry policy is out of scope.
- Packet payload/view pointers are valid **only during the synchronous callback**.
  Copy if retaining them. The `uintptr_t` token is an integer (future `cgo.Handle`),
  not a retained Go pointer. There is no async packet queue or pre-decode dropping.
- Callbacks must be bounded/nonblocking. All status-returning ABI calls reject
  callback reentry with `CALLBACK_CONTEXT`. Never call either destroy function
  from a callback (defensively ignored); the external owner must serialize destroy
  against all calls. Stop/retirement wait for callbacks, so an unbounded consumer
  can still block shutdown. Do not perform WebSocket/disk/DB I/O inside callbacks.

### Clocks and interruption

`open_timeout_ms` covers both open and stream-info using one absolute monotonic
clock deadline. `idle_timeout_ms` defaults to 5000 and is refreshed only by selected
video packets. The socket timeout uses the larger configured limit while the
interrupt callback enforces the current phase's tighter deadline. Successful
FFmpeg stream-info can still be partial after interruption, so deadlines are checked
after successful calls too. Cleanup signals cancellation **before** closing the
input (including normal probe completion/TEARDOWN). Only the owning worker touches
and closes its AVFormatContext; stop only signals it and joins.

Blocking system DNS resolution is not guaranteed interruptible by AVIOInterruptCB.
Neither these deadlines nor tests are an unconditional real-time guarantee for
arbitrary OS calls, CPU-heavy decoding during probe, or misbehaving callbacks.

Packet PTS/DTS are original stream ticks with actual `time_base`; missing timestamps
have explicit flags and zero values. Negative timestamps are not coerced to zero.
No wall-clock normalization, guessed FPS, stream synchronization or automatic
Annex-B conversion is performed. Unknown FPS is `0/1`; dimensions/codec must be
valid for probe success. Software H.264/HEVC decoders are compiled for stream-info,
not exposed as a continuous decode pipeline.

## Regression tests

```sh
make native-test
python3 native/scripts/build.py --sanitizer address test  # Engine ASan + UBSan
python3 native/scripts/build.py --sanitizer thread test   # Engine TSan
# Exclusive writer ownership required; temporarily edits/restores the ABI file:
python3 native/tests/verify_incremental.py --allow-source-edit
```

CTest includes pure C ABI/lifecycle, C++ URL/ownership/drain units, real loopback
RTSP/RTP integration, and offline build/cache/target rejection tests. Fixtures use
checked-in synthetic black-frame NALs; no system FFmpeg, camera, server download or
public network is needed to run them. Integration exercises H.264/H.265 metadata
and packets, TCP/UDP, extradata >512 bytes, real grace expiry/connection counts,
concurrent consumers, callback-context rejection, unsubscribe/stop drain, open/
stream-info/read timeout, stop during blocked network reads/probes and silent
TEARDOWN. Additional regressions exercise unregistering a callback retained by a
worker snapshot, callback exceptions, probe-result ownership after engine destroy,
and credential silence on both output streams. Build-contract tests cover ambient
search-path rejection and repeated Go output/compile-only flags. Fixtures are not
real-camera, lossy-network, or RK3568 acceptance.

Sanitizers instrument Engine/test code; the cached FFmpeg libraries are not rebuilt
with sanitizer instrumentation. UBSan is non-recovering so diagnostics fail CTest
instead of printing a warning and returning success. Allocator interception helps detect leaks, but
these runs do not certify all FFmpeg internals. The 30-second continuous decode
check from the streaming guide is inapplicable to a capture-only pipeline; this
project does not claim decode/playback correctness based on probe alone.

## Source, licensing and distribution

FFmpeg 7.1.5: <https://ffmpeg.org/releases/ffmpeg-7.1.5.tar.xz>

SHA-256: `de668509caf9e35e3cd162473441fdb29538c6d96ed080292b3cf9e6fc5d558f`.
This is an HTTPS-downloaded consistency pin, **not** an independently verified
publisher signature. No source patches are applied. The recipe disables GPL,
nonfree/version3-by-default features and external packages; final configuration
reports LGPL 2.1 or later. Enabled transitive demuxers include asf/mov/mpegts/rm
alongside RTSP; protocol selection includes HTTP via RTSP, not only TCP/UDP/RTP.
Full generated config is retained rather than claiming the requested component
list is the complete capability list.

`install/share/zhulong/` contains `COPYING.LGPLv2.1`, `LICENSE.md`, `config.h`, and
`config.mak`; the exact source archive/extraction and build log remain under the
cache. For any static binary distribution, preserve corresponding source and
modifications, configuration/toolchain instructions, notices, and the application
object/source/build materials necessary for applicable LGPL relinking rights.
Retain Engine objects/archive and Go build/relink inputs as applicable; notices
alone are **not** a complete compliance package. No board release/distribution
bundle or legal-compliance certification is produced by this task.
