# Journal - nikoniko (Part 1)

> AI development session journal
> Started: 2026-10-04

---

## Session 1: Repository and Trellis bootstrap
<!-- trellis-session: v=2 fp=baa2d89853e492a7 -->

**Date**: 2026-10-04
**Task**: Repository and Trellis bootstrap
**Branch**: `dev`

### Summary

Committed repository/Go setup and Trellis workflow/specs separately; corrected the TypeScript ignore rule and stale guidance link, then archived Bootstrap Guidelines.

### Git Commits

| Hash | Message |
|------|---------|

### Status

[OK] **Completed**


## Session 2: Apple-Style UI Overhaul and Auth UX Refactoring
<!-- trellis-session: v=2 fp=2006807dd10f453a -->

**Date**: 2026-10-05
**Task**: Apple-Style UI Overhaul and Auth UX Refactoring
**Package**: frontend
**Branch**: `feat/project-skeleton`

### Summary

Rebuilt system theme layout as an edge-to-edge macOS console with GPU dynamic aurora background, refactored login and setup wizards with Remember Me and onBlur validation

### Main Changes

- Transformed topbar into edge-to-edge frosted glass console header with minimal brand logo
- Added GPU-accelerated macOS Aurora dynamic ambient background with 24px dot matrix grid
- Refactored LoginForm and InitForm into compact macOS-styled dialogs
- Added Remember Me persistence and onBlur field-level validation with real-time error clearance

### Git Commits

| Hash | Message |
|------|---------|
| `813ec4b` | feat(frontend): overhaul Apple-style theme layout and authentication UX |

### Testing

- [OK] pnpm --prefix web test (37 passed across validation and auth flows)
- [OK] pnpm --prefix web type-check && pnpm --prefix web lint && pnpm --prefix web build
- [OK] go test ./...

### Status

[OK] **Completed**

### Next Steps

- Review and commit remaining backend error-handling changes when ready


## Session 3: 项目骨架、认证收尾与媒体接入
<!-- trellis-session: v=2 fp=1e9dac347825b643 -->

**Date**: 2026-10-05
**Task**: 项目骨架、认证收尾与媒体接入
**Package**: backend
**Branch**: `dev`

### Summary

完成项目骨架与单用户认证任务归档；提交认证错误解耦及全栈开发入口，规划并实现静态 FFmpeg RTSP 原生接入。make check 与 smoke 已通过；实际目标交叉编译和板端验证仍未完成，Native 子任务保持进行中。

### Git Commits

| Hash | Message |
|------|---------|
| `233a482` | refactor(auth): decouple domain errors with apperr, add generic endpoints, and remove rememberMe |
| `47a5b27` | build: add air live reload configuration and fullstack dev targets |
| `e84fc3d` | chore(task): add planning artifacts for media ingestion pipelines |
| `5abbe4d` | chore(task): update 10-05-native-ffmpeg-ingestion planning status |
| `0080930` | feat(native): implement C++ media engine with static FFmpeg RTSP ingestion |

### Status

[OK] **Completed**

---

**Date**: 2026-10-05
**Task**: Go/CGO 媒体桥接与订阅生命周期 (`10-05-go-cgo-media-bridge`)
**Package**: backend
**Branch**: `dev`

### Summary

完成媒体接入阶段二首个交付：实现 `internal/engine/` 中的纯 Go 门面与 CGO 跨语言桥接。
1. 封装 Native 视频流探测（`Probe`），采用独立私有 Native Engine 隔离取消，支持最多 4 路并发控制与 context deadline 收敛。
2. 封装物理 RTSP 流获取与复用（`Acquire` / `Release` / `Status`），支持连接池复用与 8 秒宽限期语义。
3. 实现按需视频包订阅（`Subscribe` / `Next`），在 CGO 回调中通过借用指针与 `unsafe.Slice` 深拷贝到 Go-owned 内存，配合有界包数与字节队列（默认 32 包 / 16 MiB 上限）提供明确的 `ErrBackpressure` 终态。
4. 保证 `cgo.Handle` 生命周期安全：仅在 Native unsubscribe 同步排空（Drain）完成后执行 `handle.Delete()`，杜绝悬空指针访问。
5. 编写测试运行器 `native/tests/run_go_bridge_tests.py` 并接入 `make go-check`，在真实 loopback RTSP 服务桩上验证 H.264/H.265、TCP/UDP、超大参数集、慢订阅背压隔离与敏感凭据脱敏。全栈 `make check`、`make smoke` 及 `GOEXPERIMENT=cgocheck2` 均 100% 通过。

### Status

[OK] **Ready for commit review**
