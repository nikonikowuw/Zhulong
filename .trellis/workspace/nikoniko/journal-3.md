# Journal - nikoniko (Part 3)

> Continuation from `journal-2.md` (archived at ~2000 lines)
> Started: 2026-11-04

---



## Session 96: 人员与人脸 OpenAPI 下发接收（M2M Bearer Token）
<!-- trellis-session: v=2 fp=8c9aec9e417dd2a2 -->

**Date**: 2026-11-04
**Task**: 人员与人脸 OpenAPI 下发接收（M2M Bearer Token）
**Package**: backend
**Branch**: `dev`

### Summary

为外部平台提供独立 /api/v1/openapi 的 Bearer Token 通道：人员按外部业务键 personId 增量 upsert、人脸细粒度增删改与原子替换，Web UI 增加 Token 管理与来源徽标，并沉淀单连接事务死锁与 M2M Token 契约

### Main Changes

- 新增 internal/apitoken：sk- 前缀 + 32B CSPRNG，仅存 SHA-256 与 masked 前缀，创建响应 Cache-Control: no-store，软吊销立即生效、名称全表唯一（含已吊销）
- OpenAPI 独立路由组 /api/v1/openapi + RequireBearer（根组 11 MiB、JSON 子组叠 1 MiB）；严禁混挂 Cookie Session，未注入 Token 服务时用 500 阻断而非免密放行
- persons.person_id 外部业务键（条件唯一 + 创建后不可变，变更返回 409 PERSON_ID_IMMUTABLE）与 source/source_name 首次录入来源；迁移 000027/000028
- 单人增量同步与细粒度人脸：upsert、addFace、replace=true 先提取后单事务替换、跨人员主照 404、10 张上限；失败路径保留旧脸并删除新文件
- 错误语义显式化：新增 apperr.KindBadRequest(400)/KindPayloadTooLarge(413)，OpenAPI 侧用 statusFor 表而非 Kind 推导；面档限流 3/1 只挂 POST /faces，避免 DELETE 被连带 429
- Web UI：设置页 Token 管理（复制/吊销/明文只显示一次）与人员列表来源徽标，三语文案；docs/openapi/person-face.md 与 swag securityDefinitions 交付
- spec 沉淀：MaxOpenConns=1 下事务内再取连接会永久挂死（不是超时）；authentication-guidelines 新增 M2M Bearer Scenario（一次性明文、401/500 分界、审计枚举原因、last_used_at 合并与 Close 排空）

### Git Commits

| Hash | Message |
|------|---------|
| `20d299e` | feat(openapi): support M2M person and face provisioning with Bearer auth |
| `6d79060` | refactor(person): align error semantics and address code review findings |
| `30b22a8` | docs(spec): record the single-connection and M2M token contracts |

### Testing

- [OK] make check 通过：前端真实无头浏览器 151 文件 / 1079 用例、native-test 31/31、api-docs、go-check
- [OK] go test -race ./internal/apitoken/... ./internal/person/... ./internal/openapi/...：生成/哈希/并发上限/同名含吊销冲突/吊销幂等/鉴权矩阵/last_used_at 合并与优雅排空
- [OK] 手工冒烟：无后缀上传 201、10.5 MiB → 413 FILE_TOO_LARGE、超大 JSON → 413 PAYLOAD_TOO_LARGE、并发超额 429 + Retry-After、吊销后 401 且有 api_token.auth_rejected 审计
- [OK] 日志与审计 detail 中搜索原始 Token 字符串零命中

### Status

[OK] **Completed**

### Next Steps

- 可信专网部署约束落地；TLS/网关作为后续部署选项，不在本期
- faceextract 串行点不可取消（c.mu.Lock 等待不看 ctx），若需让排队本身可取消应另立任务
- Web UI 的 SearchByImage 坏图仍返回 400，与 OpenAPI 422 不一致，另立任务统一


## Session 97: Storage emergency self-heal and RK3568 verification
<!-- trellis-session: v=2 fp=e09a827a3551e8e8 -->

**Date**: 2026-11-04
**Task**: Storage emergency self-heal and RK3568 verification
**Package**: backend
**Branch**: `dev`

### Summary

Implemented the storage emergency self-heal fix: used/(used+free) usage accounting, two-phase TTL/capacity pruning with an emergency target, grouped snapshot eviction with lock/alarm protection, pending_snapshot_deletes outbox plus record cascade, cleanup backoff/audit, and matching UI copy. make check passed. Deployed the aarch64 build (19e982fe) to the RK3568 board, migrated schema 28->29, and verified emergency cleanup: 14,608 files/2.65GB freed, then watermark cleanup to 71% freed another 4.02GB; pending rows drained to 0 and writes resumed.

### Git Commits

| Hash | Message |
|------|---------|
| `0ab2ad7` | fix(storage): self-heal emergency cleanup and cascade snapshot deletes |

### Status

[OK] **Completed**


## Session 98: Finish alert-push task
<!-- trellis-session: v=2 fp=8c1e0427cfd107c6 -->

**Date**: 2026-11-06
**Task**: Finish alert-push task
**Package**: backend
**Branch**: `dev`

### Summary

归档告警上报模块与多通道分发任务；保留进行中的 crop-region 任务。未提交的 .trellis/.template-hashes.json 与本次归档无关，未改动。

### Git Commits

| Hash | Message |
|------|---------|
| `36a936a` | feat(alertpush): add multi-channel event dispatch with host identity |
| `1501b46` | feat(web/settings): expose api-tokens and alert-push in sidebar and polish system status ui |
| `e1968bc` | fix(web/settings): let token and alert-push pages fill the settings pane |
| `464baa0` | refactor(web/alert-push): streamline destination dialog with single-flow progressive disclosure |
| `c474103` | refactor(alert-push): optimize render performance and enhance type safety |

### Status

[OK] **Completed**


## Session 99: Archive face-recognition CCTV optimization and 4 completed tasks
<!-- trellis-session: v=2 fp=96ebc8b48fbd4114 -->

**Date**: 2026-11-12
**Task**: Archive face-recognition CCTV optimization and 4 completed tasks
**Package**: native
**Branch**: `dev`

### Summary

归档 11-06-face-recognition-cctv-optimize，并一并清理归档已完成合入 dev 的 4 个任务（11-06-tripwire-config、11-08-face-recognition-feature-fusion、11-11-streaming-face-feature-persistence、11-11-outbound-heartbeat-report）；保留进行中的 11-03-task-crop-region 与规划中的 11-12-stranger-cluster-trajectory。

### Main Changes

- 归档 11-06-face-recognition-cctv-optimize：像素空间眼线几何质检、对齐脸块清晰度/曝光可选门禁、按 person_id 身份聚合与分差、SCRFD pre/post-NMS 分离预算及 .env 配置收敛
- 归档 11-06-tripwire-config：任务级带方向绊线编辑器、校验契约与算法隔离 IPC/SDK 只读下发
- 归档 11-08-face-recognition-feature-fusion：按流有界双重余弦门禁跨帧特征融合与宿主同轨迹 unmatched→matched 冷却升级落库
- 归档 11-11-streaming-face-feature-persistence：流式 512 维人脸特征 SDK Arena/IPC Base64 回传与 recognitions.feature_vector 持久化
- 归档 11-11-outbound-heartbeat-report：推送目标事件/存活正交双能力、MQTT LWT 遗嘱与 GET /api/v1/openapi/status 状态拉取端点

### Git Commits

| Hash | Message |
|------|---------|
| `d4e7d32` | feat(task): add tripwire configuration and SDK region delivery |
| `7c66dcc` | feat(algo/face): fuse features across frames and gate faces on image quality |
| `8c57267` | feat(record): let a tracked face upgrade from stranger to matched within one cooldown window |
| `5c114de` | refactor(algo/face): streamline config schema to core params and support .env overrides |
| `3d069f0` | feat(face): 回传 512 维人脸特征并持久化到识别记录 |
| `77bf723` | feat(alertpush,openapi): 新增外部平台心跳上报与设备状态端点 |

### Status

[OK] **Completed**

### Next Steps

- 11-03-task-crop-region 待完成板端 spike 与 RGA 第二刀验证
- 11-12-stranger-cluster-trajectory 在另一窗口继续规划推进
