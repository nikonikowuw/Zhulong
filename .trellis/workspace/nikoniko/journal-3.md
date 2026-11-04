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
