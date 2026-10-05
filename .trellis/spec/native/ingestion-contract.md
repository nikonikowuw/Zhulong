# Native ingestion and static build contract

## 1. Scope / Trigger

Changes to `native/include/Zhulong/engine.h`, RTSP capture/lifecycle, static
FFmpeg dependency selection, CMake, CGO linking, or cross-target selection require
these regression checks. Go camera business logic, decoding, recording,
VPU/NPU and board support are outside the current implementation.

## 2. Signatures

Exact C-compatible types/signatures live in the sole public header `engine.h`:

- Existing: `Zhulong_engine_create/start/stop/destroy` (old statuses preserved).
- `Zhulong_engine_probe(engine, url, options, out_result)`;
  `Zhulong_probe_result_view(result, out_view)`; `Zhulong_probe_result_destroy(result)`.
- `Zhulong_stream_acquire(engine, url, options, consumer_id, consumer_kind, out_stream)`;
  `Zhulong_stream_release(engine, stream, consumer_id)`;
  `Zhulong_stream_get_status(engine, stream, out_status)`.
- `Zhulong_stream_subscribe(engine, stream, preview_consumer_id, callback, uintptr_t_token, out_subscription)`;
  `Zhulong_stream_unsubscribe(engine, stream, subscription)`.
- Build: `make native-deps` (explicit network); `make native-build/native-test/go-check`;
  `python3 native/scripts/build.py go build|test|vet ...`.
- Cross: `make native-cross-build CROSS_PROFILE=... CROSS_OUTPUT=build/targets/NAME/Zhulong`.
  Profile fields: `name`, `cc`, `cxx`, `ar`, `ranlib`, `sysroot`, `arch`, `goarch`,
  `target_os: linux`, optional `cflags`/`ldflags` arrays; 32-bit ARM requires `goarm`.

## 3. Contracts

- `options == NULL` or zero timeout fields select TCP / 5000ms open / 5000ms idle.
  Open and stream-info share one monotonic deadline; idle resets only on selected
  video packets. Even successful stream-info returns must be checked for expired
  deadline/cancellation (FFmpeg can return partial metadata after interruption).
- Only the input-owning thread accesses/closes AVFormatContext. Cancel **before**
  close even for a successful synchronous probe: TEARDOWN may stop responding.
  Stop signals all workers/probes, joins the timer/workers, and drains callbacks.
  System DNS and arbitrary blocking user callbacks are not hard-interruptible.
- Destroy is externally serialized with every other operation, including probe.
  No control/probe/destroy API from callbacks; status functions reject callback
  context before taking control locks. Void destroy defensively ignores callback
  invocation, which is forbidden and must be corrected by the owner.
- A stream is deduplicated by normalized valid RTSP URI. Only protocol/host/default
  port/outer whitespace change. Credentials/path/query preserve semantics. Duplicate
  consumer IDs never increment references; transport/timeouts must match.
- Preview subscription needs an acquired Preview consumer; it adds no reference.
  One subscription per consumer. Unsubscribe prevents new callback admission and
  drains any in-flight invocation, including a worker's earlier snapshot.
  Release requires unsubscribe first. Zero consumers start an 8-second grace;
  reacquire cancels expiry. Stop invalidates IDs. Failed streams do not reconnect;
  reacquire during grace keeps their failed state until all references expire.
- Native owns result and packet buffers. Probe-result view lasts until result
  destroy, independent of engine lifetime. Packet view lasts only during callback.
  A retained packet needs a consumer copy. Integer callback tokens are not Go
  pointers; future Go code deletes `cgo.Handle` only **after** successful drain.
- PTS/DTS retain original ticks/time_base, with explicit absence flags and zero
  value when absent. Do not convert missing timestamps into valid zero timestamps.
  Unknown FPS is 0/1; dimensions/codec must be valid. Extradata is dynamic, may be
  missing, is never truncated and does not imply Annex B. No decode/GOP dropping.
- Raw FFmpeg logging is disabled process-wide by engine initialization because it
  can disclose URLs/credentials. Only controlled error codes leave capture.
- FFmpeg 7.1.5 SHA-256 is
  `de668509caf9e35e3cd162473441fdb29538c6d96ed080292b3cf9e6fc5d558f`.
  Native configure/build never fetches; explicit preparation accepts HTTPS or a
  local archive. No shared FFmpeg fallback. Only avformat/avcodec/avutil archives
  plus system dependencies from their target `.pc` files reach the final Go link.
- Cache identity includes version/source/recipe/resolver, target, flags, compiler
  binaries/version/backend/runtime inputs and sysroot content. Sysroot symlinks
  escaping that tree are rejected rather than reading untracked host SDK inputs.
  Host/cross trees never overwrite each other. Ambient compiler search paths
  (`CPATH`, `C*_INCLUDE_PATH`, `LIBRARY_PATH`, `COMPILER_PATH`, `GCC_EXEC_PREFIX`),
  SDK/deployment overrides and CGO/compiler flags are rejected before tool discovery:
  CMake root-path rules alone do not isolate compiler-driver search paths.
  CMake receives the profile's `ar` and `ranlib` explicitly, not just its compilers.
  Explicit CMake flags (not merely `*_FLAGS_INIT`) prevent stale ABI flags when a
  profile changes in an existing build directory. Cross Go commands require one
  checked `-o`/`-o=...` and, for test compilation, one enabled `-c`; repeated flags
  must not override the checked output or turn compilation into test execution.
- Go does not hash external archives: link an immutable archive snapshot named by
  its content digest, so changed Native code changes CGO_LDFLAGS and cache identity.
  Bare Go commands no longer select a default Native archive. Make/Air use wrapper.
- C++17 feature/link checks, not an artificial GCC minimum. Cross tests compile
  but never execute on the host. Missing SDK fails closed. Source toolchain support,
  target linking and board runtime compatibility are separate acceptance layers.

## 4. Validation & Error Matrix

| Condition | Expected |
| --- | --- |
| Null engine/output, invalid transport/consumer/URI | INVALID_ARGUMENT; output ID/result cleared |
| Engine not started/stopped | NOT_RUNNING |
| Duplicate consumer/subscription | DUPLICATE; no added reference/registration |
| Same URI, different transport/timeouts | CONFIG_CONFLICT |
| Release with subscription | BUSY; unsubscribe then retry |
| Missing consumer/stream/subscription | NOT_FOUND (or NOT_RUNNING after stop) |
| Non-Preview subscription | INVALID_ARGUMENT |
| Unsupported codec/no valid dimensions | UNSUPPORTED; no probe result |
| Open/info/read deadline | TIMEOUT; safe error code, no raw URL text |
| Stop during registered probe | CANCELLED; stop waits for probe cleanup |
| Callback tries status-returning ABI | CALLBACK_CONTEXT; no self-wait |
| Last consumer released then acquired before 8s | Same ID/connection, no extra physical open |
| Last reference expired | Timer cancels/joins before pool erasure; new ID on new acquire |
| Missing/corrupt source/static prefix | Build error; no download/shared fallback |
| Host libraries passed to cross CMake | Configuration failure |
| Cross Go execution or host output path | Rejected before building |
| ABI source changed | Only affected object(s), archive, Native tests and Go final link rebuild; unchanged FFmpeg reused |

## 5. Good / Base / Bad Cases

- Good: prepare once; use wrapper for Native and Go; acquire → subscribe → bounded
  callback copying borrowed data → unsubscribe/drain → release → stop → destroy.
- Base: Start/Stop/Close remain idempotent for the current Go lifecycle bridge.
  No subscriptions means no packet callbacks; result ownership is independent.
- Bad: `-Lbuild/native -lZhulongEngine` forever, host `.pc` lookup during cross,
  treating a static CMake target as a merged FFmpeg archive, deleting callback
  token before drain, guessing timestamps/FPS, or claiming host tests certify RK3568.

## 6. Tests Required

`make native-test` runs 5 CTests: lifecycle, pure C ABI, capture units, loopback
RTSP/RTP integration, build contract (12 Python cases). Assert H.264/H.265 metadata/
packet codec, TCP and UDP, time-base/PTS step, extradata >512 bytes, credential
silence, one physical connection, actual grace cancellation/expiry, duplicate and
conflict handling, concurrent consumers, callback reentry rejection, unsubscribe/
stop drain, open/info/read interruption and cancelled probe cleanup. Include
unregistering the second callback from a real worker snapshot blocked in the first,
a throwing C++ callback followed by successful drain, and probe-view data surviving
engine destruction. Credential assertions inspect both stdout and stderr.

Run Engine ASan/UBSan and TSan in separate directories. UBSan compilation must
include `-fno-sanitize-recover=undefined` so a diagnostic cannot exit zero and be
mistaken for a passing CTest. FFmpeg archives themselves
are not sanitizer-instrumented. `make check` includes frontend checks and Go vet/
race; `make smoke` builds/runs the real host. The opt-in, exclusive-writer
`python3 native/tests/verify_incremental.py --allow-source-edit` inserts and restores
an ABI marker, checks it enters/exits the actual Go binary, and verifies unchanged
FFmpeg hashes/mtimes and unaffected object files. Do not run it with another writer.

Inspect final executable ELF/NEEDED/version-info and archive member architecture.
Cross build, target ELF inspection and board runtime remain pending without SDK/
board; simulated cross rejection tests are not cross compilation acceptance.
See `native/README.md` for source/licensing preservation and distribution limits.

## 7. Wrong vs Correct

Wrong:
```cpp
subscriptions.erase(id); // worker snapshot may still invoke the removed token
// caller deletes cgo.Handle here -> use-after-free
```
Correct:
```cpp
// Remove under stream lock; release that lock before callback drain.
removed->disable_and_drain(); // prevents new entry, waits for active invocation
// Only now may the caller delete its token.
```

Wrong: reset the probe timeout after `avformat_open_input`, close context from the
stop thread, or accept partial stream-info success after the deadline.
Correct: one absolute open/info deadline; stop only sets cancellation; owning
worker checks deadline and closes its context after cancellation.
