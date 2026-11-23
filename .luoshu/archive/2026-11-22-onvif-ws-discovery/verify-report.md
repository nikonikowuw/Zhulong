# Verify Report: 2026-11-22-onvif-ws-discovery

- Verdict: PASS
- base_commit: b199f1e926e65219b3fcfe4e435803a05b926e1c
- Worktree: /home/niko/dev/Go/Zhulong/.luoshu/worktree/2026-11-22-onvif-ws-discovery

## Gate Results
| Gate | Command | Status | Duration |
| --- | --- | --- | --- |
| Discovery unit tests (`-race`) | `go test -count=1 -race ./internal/camera/discovery -v` | PASS (9/9) | 1.341s |
| Camera handler discover test (`-race`) | `python3 native/scripts/build.py go test -count=1 -race ./internal/camera -run 'TestCameraHandlerDiscover\|TestHandler_Discover' -v` | PASS | 1.058s |
| Go format, vet & domain tests (`-race`) | `test -z "$(gofmt -l cmd internal)" && python3 native/scripts/build.py go vet ./internal/camera/... ./internal/audit/... && python3 native/scripts/build.py go test -race ./internal/camera/... ./internal/audit/...` | PASS | ~11s |
| Frontend browser tests (Chromium) | `cd web && pnpm test -- src/features/cameras/api/cameras-api.test.ts src/features/cameras/components/cameras-discover-dialog.test.tsx` | PASS (9/9) | 2.49s |
| Frontend format, lint & typecheck | `cd web && pnpm run format:check && pnpm run lint && pnpm exec tsc --noEmit` | PASS (0 errors, 2 pre-existing warnings) | ~15s |
| API docs generation & check | `make api-docs && grep -n "cameras/discover" internal/apidocs/swagger.json internal/apidocs/swagger.yaml internal/apidocs/docs.go` | PASS | ~6s |
| Locale key parity (`en`, `zh-Hans`, `zh-Hant`) | Python key diff across `web/src/features/{cameras,audit}/locales/*.json` | PASS (0 missing, 0 extra) | <1s |

## Contract & Checklist Evaluation
- [PASS] V1 (WS-Discovery 协议与 XML 编解码正确性): `internal/camera/discovery/soap.go:28-55` constructs SOAP 1.2 Probe with `ProbeAction` and `dn:NetworkVideoTransmitter`; `ParseProbeMatches` (`soap.go:80-99`) extracts `EndpointReference`, `XAddrs`, URL-unescaped `Scopes`, IP/port, and handles malformed/empty XML without panic (`TestSOAP*` 4/4 pass).
- [PASS] V2 (多网口有界探测与并发保护): `resolveOutboundAddresses` (`internal/camera/discovery/discoverer.go:253-297`) filters `FlagUp` non-loopback IPv4 interfaces; `executeProbe` and `probeOnConn` (`discoverer.go:104-251`) bind per-interface UDP sockets and send/receive on the same `net.PacketConn` so unicast `ProbeMatches` replies hit the listening source port; `probeOnConn` (`discoverer.go:201-212`) sets `conn.SetDeadline` before starting the context-cancel watcher goroutine; `Discover` (`discoverer.go:57-102`) clamps timeout to `[3s, 10s]` and uses `singleflight.Group` + 5s cache. `TestDiscovererMockProbeResponse` (`discoverer_test.go:125-195`) exercises `UDPDiscoverer.Discover` end-to-end against a mock UDP responder and passes alongside timeout/cancel/singleflight tests (`-race`).
- [PASS] V3 (HTTP API 契约与审计合规性): `POST /api/v1/cameras/discover` registered before `/:id` in `internal/camera/handler.go:71` (`protected_routes`), returns `httputil.Success` envelope (`handler.go:852-880`), tags `audit.ActionCameraDiscover`, and `make api-docs` succeeds with `/cameras/discover` present in `internal/apidocs/{swagger.json,swagger.yaml,docs.go}`.
- [PASS] V4 (前端交互、表单回填与三语 i18n): `CamerasDiscoverDialog` (`web/src/features/cameras/components/cameras-discover-dialog.tsx:26-42,65,107-108`) provides `useScanCountdown` live countdown indicator during scanning, renders discovered device cards, and prefills `CamerasActionDialog` on click; `en`, `zh-Hans`, `zh-Hant` locale files have 0 missing/extra keys; real Chromium browser tests pass (9/9).
- [PASS] V5 (负向边界): `CameraService.Create` (`internal/camera/service.go:233-241`) still enforces `s.prober.ValidateStreams` dual-stream gate; `git diff b199f1e926e65219b3fcfe4e435803a05b926e1c --stat -- native internal/engine internal/database/migrations` confirms zero changes to `native/`, `internal/engine/`, or `internal/database/migrations/`; no TCP/SYN port scanning.
- [PASS] Tasks completion: All 7 tasks in `.luoshu/changes/2026-11-22-onvif-ws-discovery/tasks.md` are checked `[x]` (`incompleteTaskIds: []`).

## Rules & Specs Compliance
- Matched / Model-Decision Rules:
  - `go-host/http-contract` & `go-host/route-auth-boundary`: Compliant (`httputil.Success` / `apperr.Error`, `protected_routes`, `audit.ActionCameraDiscover`, Swagger annotations).
  - `go-host/concurrency-and-errors` & `go-host/code-style`: Compliant (bounded socket lifecycle, same-socket send/receive, pre-set deadline before cancel watcher, `-race` clean).
  - `go-host/test-wiring`: Compliant (`native/scripts/build.py go test -race` used for `internal/camera`).
  - `web/frontend-api-boundary` & `web/frontend-browser-tests`: Compliant (`discoverCamerasResponseSchema.parse` in `cameras-api.ts`, real Chromium tests in `cameras-discover-dialog.test.tsx`).
- Mounted Specs:
  - `architecture.md` & `backend/camera-guidelines.md`: Compliant (pure Go control-plane discovery, dual-stream gate preserved, `.luoshu/specs/backend/camera-guidelines.md` updated).
  - `backend/http-api-guidelines.md`, `frontend/development-guidelines.md`, `frontend/theme-and-i18n.md`: Compliant.

## Findings
- [Note] `Makefile` — Adds `if command -v swag` fallback in `api-docs` target (benign, outside `tasks.md` file list).
