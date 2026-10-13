# 修复算法包生命周期 P0 缺陷与规范偏差 — 实施计划 (Implement)

## 前置约定

- **任务目录**：`.trellis/tasks/10-12-algo-lifecycle-remediation/`
- **分支**：`dev`（base_branch 亦为 `dev`）
- **构建入口**：⚠️ **必须用 `python3 native/scripts/build.py go <cmd>`**，不能用裸 `go`。裸跑会因 native 静态库未进链接而报 `undefined reference to Zhulong_*`（这是构建环境特性，非代码缺陷）。
- **顺序原则**：每阶段末尾的验证必须通过才进入下一阶段。失败即回滚该阶段，不带病前进。

### 实施顺序总览

| 阶段 | 内容 | 依赖 | 验证命令 |
| --- | --- | --- | --- |
| 1 | 契约文档先行（broker 语义、退出码、manifest 字符集、未实现登记） | 无 | 人工核对 |
| 2 | R3 manifest 路径安全（含回归测试） | 无 | `go test -race ./internal/algorithm/` |
| 3 | R4 IPC 门禁 + 枚举注释 + 跨语言常量测试 | 无 | 同上 |
| 4 | R5 `.env` 接线（端到端） | 无 | 同上 |
| 5 | R7 熔断阈值定案 | 无 | 同上 |
| 6 | R2 broker 目录语义 + 失败阶段可观测性 | 阶段 1 | 同上 |
| 7 | R1 上传路由与流式改造 | 无 | `go test -race ./internal/app/ ./internal/algorithm/` |
| 8 | R6 热更新澄清 + 死代码标注 | 无 | 人工核对 |
| 9 | 全量门禁 | 全部 | `make go-check` / `make native-test` |

> 阶段 1–6 与 8 相互独立，可并行；阶段 7 独立但风险最高，放在后段以便单独复核。

---

## 阶段 1：契约文档先行

**目的**：把跨语言约定写死，避免实现与文档再次漂移（R2/R3/R4 都依赖它）。

- [x] 1.1 更新 `.trellis/spec/native/algo-plugin-contract.md`：
  - 新增 **`--broker` 参数语义 = 目录**（不是 socket 文件路径），并引用 `frame_broker.cpp:20` 的派生规则；
  - 新增 **runner 退出码表**：`0` 正常 / `1` dlopen 失败 / `2` model_create 失败 / `3` worker_create 失败 / `4` task_open 失败 / `5` **broker 连接失败**；
  - 新增 **manifest 标识字段字符集约束**：`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`；
  - 新增 **未实现登记**章节：「FrameBroker 宿主进程与帧发布通路未实现；`FrameBroker` 仅在 `native/tests/frame_broker_test.cpp` 中被实例化」，并标注由 `10-10-algorithm-plugin-architecture` 承接；
  - 在 §6.4 补充 Go 侧 `EncodeIpcMessage`/`DecodeIpcHeader` 的门禁要求。

**验证**：人工核对上述五项均已写入，且 broker 语义与 `frame_broker.cpp:20` 一致。

**回滚点**：文档改动，`git checkout` 即可。

---

## 阶段 2：R3 manifest 路径安全

**目的**：封堵已实测确认的 `filepath.Join` 逃逸。

- [x] 2.1 `internal/algorithm/verifier.go` 新增错误码与校验：
  ```go
  var ErrInvalidManifestToken = errors.New("manifest field contains illegal characters")
  var manifestTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
  ```
  在 `VerifyDirectory` 的非空校验（`:98`）之后，对 `manifest.ID` / `manifest.Version` / `manifest.Name` 逐一校验。
- [x] 2.2 `StageAndVerify`（`:228`）增加**第二层前缀断言**：
  ```go
  targetDir := filepath.Join(finalInstallDir, manifest.ID, manifest.Version)
  absBase, err := filepath.Abs(finalInstallDir)
  absTarget, err := filepath.Abs(targetDir)
  if err != nil || !strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) {
      return nil, fmt.Errorf("%w: resolved install path escapes the install root", ErrInvalidManifestToken)
  }
  ```
  写法与 `uploader.go:179` 的 Zip-Slip 断言保持一致。
- [x] 2.3 `internal/algorithm/locale.go` 注册 `INVALID_MANIFEST_TOKEN` 的 en / zh-Hans / zh-Hant 三语文案。
- [x] 2.4 `verifier_test.go` 新增表驱动回归测试：
  - **拒绝**：`../../../tmp/pwned`、`..`、`.`、`/abs/path`、`a/b`、`a\b`、`""`、`-leading-dash`、65 字符超长、含空格/中文；
  - **通过**：`yolov8-person-v2`、`1.0.0`、`v1.2.3-rc.1`、`a`。
- [x] 2.5 断言 `StageAndVerify` 对恶意 id **不创建** `installDir` 之外的任何目录（用 `t.TempDir()` 作为 `finalInstallDir`，校验父目录未被写入）。

**验证**：
```bash
python3 native/scripts/build.py go test -race ./internal/algorithm/ -run 'Manifest|Verify|Stage' -count=1 -v
```

**回滚点**：`verifier.go` 两处 + 测试文件，独立可回滚。

---

## 阶段 3：R4 IPC 门禁与契约对齐

**目的**：落实 64KB 门禁；防止枚举再次漂移。

- [x] 3.1 `internal/algorithm/process.go` 新增：
  ```go
  const MaxIpcPayloadSize = 64 * 1024
  var ErrIpcPayloadTooLarge = errors.New("IPC payload exceeds the 64KB protocol limit")
  ```
- [x] 3.2 `EncodeIpcMessage` 改为返回 `([]byte, error)`，超限返回 `ErrIpcPayloadTooLarge`。
- [x] 3.3 `DecodeIpcHeader` 增加 `PayloadSize > MaxIpcPayloadSize` 校验（接收侧门禁）。
- [x] 3.4 三个 gallery 构造器（`:355-378`）透传新错误。
- [x] 3.5 **修正既有测试**：所有调用 `EncodeIpcMessage` 的地方适配双返回值。
- [x] 3.6 在 `MsgGallerySync/Upsert/Delete` 常量块加注释：native 侧 `ipc_protocol.hpp:14-20` **尚未定义** 10/11/12，实现 R6 前必须先补齐。**不改数值**。
- [x] 3.7 新增 `internal/algorithm/ipc_contract_test.go`：读取 `native/src/algorithm/ipc/ipc_protocol.hpp`，正则提取 `IPC_MAGIC` / `IPC_VERSION` / `MAX_IPC_PAYLOAD_SIZE`，与 Go 侧常量逐一比对。**文件不存在时 `t.Skip`**。
- [x] 3.8 新增门禁测试：恰好 64KB 通过；64KB+1 拒绝；`DecodeIpcHeader` 对超大 `PayloadSize` 拒绝。

**验证**：
```bash
python3 native/scripts/build.py go test -race ./internal/algorithm/ -run 'Ipc|Gallery' -count=1 -v
```

⚠️ **风险**：`EncodeIpcMessage` 签名变更会波及所有调用点与测试。先 `grep -rn "EncodeIpcMessage" internal/` 列全，一次性改完。

**回滚点**：`process.go` 常量与三个函数 + 测试文件。

---

## 阶段 4：R5 `.env` 接线

**目的**：消除死代码，让 PRD R2 的 `.env` 注入真正生效。

- [x] 4.1 `internal/algorithm/supervisor.go` 两处 `ProcessConfig` 构造（`StartPackage` 的 `:72`、`UpgradePackage` 的 `:147`）补上：
  ```go
  EnvFile: pkg.EnvPath(s.algoDir),
  ```
- [x] 4.2 确认 `process.go:176` 的「文件不存在则忽略」语义保持（`.env` 为可选，`model.go:80` 已注明）。
- [x] 4.3 `process_test.go` 新增端到端测试：
  - 在 `t.TempDir()` 写一个可执行 shell 脚本作为 `RunnerPath`：
    ```sh
    #!/bin/sh
    echo "ZHULONG_ENV_PROBE=$ZHULONG_ENV_PROBE"
    sleep 0.3
    ```
  - 同目录写 `.env` 含 `ZHULONG_ENV_PROBE=injected`；
  - 用 `zaptest/observer` 捕获日志，断言含 `ZHULONG_ENV_PROBE=injected`。
  - ⚠️ 必须走**真实 `os/exec` 环境注入**路径，不得只测 `LoadDotEnv`。
- [x] 4.4 补一个「无 `.env` 时不报错」的用例。

**验证**：
```bash
python3 native/scripts/build.py go test -race ./internal/algorithm/ -run 'Env|DotEnv' -count=1 -v
```

---

## 阶段 5：R7 熔断阈值定案

**目的**：消除 off-by-one，使 PRD「最多允许崩溃 3 次」字面成立。

- [x] 5.1 `internal/algorithm/recovery.go:104` 改为 `>`：
  ```go
  if len(sm.crashHistory) > sm.opts.MaxCrashes {
  ```
- [x] 5.2 更新 `recovery_test.go:38-46`：第 3 次崩溃**应重启**（退避 4s），第 4 次**熔断**。
- [x] 5.3 检查其他测试是否依赖旧语义（`grep -rn "MaxCrashes" internal/`）。
- [x] 5.4 在 `recovery.go` 的 `RecordCrash` 上方加注释，说明 `MaxCrashes` 语义为「窗口内允许的崩溃次数」、8s 档在 3 次配额下不可达。
- [x] 5.5 修正 `10-12-algo-package-lifecycle/design.md` 中「超过阈值」的措辞，与 PRD「最多允许」对齐。

**验证**：
```bash
python3 native/scripts/build.py go test -race ./internal/algorithm/ -run 'Recovery' -count=1 -v
```

---

## 阶段 6：R2 broker 目录语义 + 失败可观测性

**目的**：契约定案 + 让「宿主缺失」可区分（不实现宿主）。

- [x] 6.1 `internal/algorithm/module.go:76` 默认值改为目录：
  ```go
  brokerSock = "/tmp"   // 目录，非 socket 文件路径；见 native/algo-plugin-contract.md
  ```
  保留 `ZHULONG_FRAME_BROKER_SOCK` 覆写。
- [x] 6.2 `process.go` 新增失败阶段识别（**双通道**）：
  ```go
  const FailureStageBrokerConnect = "connect"
  const runnerExitBrokerUnavailable = 5
  ```
  - 主通道：`streamLogs` 扫描 stderr 中的 `"stage":"connect"`，记录 `p.failureStage`；
  - 备通道：`watchExit` 中 `exitCode == runnerExitBrokerUnavailable` 同样判定。
- [x] 6.3 `watchExit` 在 `failureStage == FailureStageBrokerConnect` 时输出 `Error` 级日志，字段含 `reason=broker_host_unavailable`、`broker_dir`、`package`。
- [x] 6.4 ⚠️ **不改变**看门狗的重启/熔断行为（仍按崩溃处理），不伪造成功。
- [x] 6.5 新增测试：用 `RunnerPath` 指向**真实** `zhulong_algo_runner`（路径由 `ZHULONG_ALGO_RUNNER` 或 `build/native/host/zhulong_algo_runner` 解析），传一个不存在的 broker 目录 + 一个 dummy plugin，断言：
  - 退出码为 5；
  - 日志中可识别 `broker_host_unavailable`；
  - **不要求**连接成功（宿主未实现）。
  - ⚠️ 若测试环境无该二进制，`t.Skip` 并注明原因。

**验证**：
```bash
python3 native/scripts/build.py go test -race ./internal/algorithm/ -run 'Broker|FailureStage' -count=1 -v
```

---

## 阶段 7：R1 上传路由与流式改造 ⚠️ 最高风险

**目的**：让 500 MB 上传在真实路由下可达，并恢复单遍流式。

- [x] 7.1 `internal/httpmiddleware/http.go` 参数化：
  ```go
  func MaxBodyLimitWithLimit(limit int64) gin.HandlerFunc { /* 原逻辑，limit 参数化 */ }
  func MaxBodyLimit() gin.HandlerFunc { return MaxBodyLimitWithLimit(MaxAPIRequestBodyBytes) }
  ```
- [x] 7.2 `internal/app/router.go`：
  - `RouterParams` 新增 `UploadRoutes []RouteRegistrar \`group:"upload_routes"\``；
  - 建立**同级**上传组（⚠️ 必须 `router.Group` 而非 `api.Group`，否则继承 1 MiB）：
    ```go
    upload := router.Group("/api/v1")
    upload.Use(httpmiddleware.AccessLog(p.Logger),
        httpmiddleware.MaxBodyLimitWithLimit(algorithm.MaxUploadSize))
    if p.AuthService != nil {
        upload.Use(auth.RequireAuth(p.AuthService))
    }
    for _, r := range p.UploadRoutes { r.RegisterRoutes(upload) }
    ```
- [x] 7.3 `internal/algorithm/handler.go` **拆分注册入口**（⚠️ 避免路由重复注册导致 gin panic）：
  - `RegisterRoutes` 保留除 install 外的全部路由；
  - 新增 `RegisterUploadRoutes(rg)`，只注册 `POST /algorithms/install`。
- [x] 7.4 `internal/app/app.go`：`algorithmOut` 增加 `UploadRoutes RouteRegistrar \`group:"upload_routes"\``，并在 `provideAlgorithm` 中**两个字段都赋值** `comp.Handler`（照抄 `networkOut` 双 group 模式，`app.go:193-212`）。
- [x] 7.5 `internal/algorithm/uploader.go`：让上限可注入以便测试（`MaxUploadSize` 为常量，500 MB 实测不可行）：
  - 保留 `SaveUploadStream(r io.Reader, tempDir string) (string, error)` 为薄封装；
  - 新增 `SaveUploadStreamWithLimit(r io.Reader, tempDir string, limit int64) (string, error)`。
- [x] 7.6 `handler.go` 的 `Install` 改用**单遍流式**解析（弃用 `c.FormFile`）：
  ```go
  reader, err := c.Request.MultipartReader()
  for {
      part, err := reader.NextPart()
      if errors.Is(err, io.EOF) { break }
      if part.FormName() != "file" { continue }
      tmpPath, err = SaveUploadStream(part, "")
      break
  }
  ```
- [x] 7.7 新增测试（**必须用真实路由**）：
  - 在 `internal/app` 包内构造 `newRouter(RouterParams{... UploadRoutes: ...})`，上传 ≥ 8 MiB 合法归档 → 断言**非 413**；
  - 未认证上传 → 401（验证 auth 中间件在新组生效）；
  - 超限用例在 `algorithm` 包内用 `SaveUploadStreamWithLimit` 小上限覆盖，断言 `ErrFileTooLarge`。
- [x] 7.8 ⚠️ 回归确认：`TestRouterSeparatesAPIAndSPA`（`router_test.go`）与 `TestHandleJSON_MaxBodyLimit`（`endpoint_test.go:157`）仍通过——通用 JSON 组的 1 MiB 上限**不得**被放宽。

**验证**：
```bash
python3 native/scripts/build.py go test -race ./internal/app/ ./internal/algorithm/ ./internal/httpmiddleware/ -count=1 -v
```

**回滚点**：本阶段独立于其它阶段，可整体 `git checkout` 这 5 个文件。

---

## 阶段 8：R6 热更新澄清 + 死代码标注

**目的**：如实记录缺口，防止下个任务误判完成度。

- [x] 8.1 `internal/algorithm/process.go` 为三个 gallery 构造器加 `⚠️ NOT WIRED` 注释，说明无调用方、传输通道未定、native 无对应消息类型。
- [x] 8.2 `internal/person/service.go:189` 的 `GetGallerySyncData` 加同样注释。
- [x] 8.3 在 `10-12-algo-package-lifecycle/prd.md` 的 R3 条目标注「**未完成**」，并指向本任务 R6 的澄清结论。
- [x] 8.4 把 R6 的澄清结论（触发时机、通道待定、消息序列、ACK、分片必要性、字段名统一为 `features`）写入 `design.md` §6 与 `native/algo-plugin-contract.md`。
- [x] 8.5 统一字段名口径：确认 `features` 为最终名（`person/model.go:66` 的 `feature_vector` 属 person 领域 DTO，**不强行改名**，但在契约文档中注明两者映射关系）。

**验证**：人工核对；`grep -rn "NOT WIRED" internal/` 应命中 4 处。

---

## 阶段 9：全量门禁

- [x] 9.1 格式与静态检查：
  ```bash
  gofmt -l cmd internal        # 期望：无输出
  ```
- [x] 9.2 全量测试（含 race）：
  ```bash
  python3 native/scripts/build.py go vet ./cmd/... ./internal/...
  python3 native/scripts/build.py go test -race ./cmd/... ./internal/...
  ```
- [x] 9.3 桥接集成测试：
  ```bash
  python3 native/tests/run_go_bridge_tests.py
  ```
- [x] 9.4 native 测试：
  ```bash
  make native-test
  ```
- [x] 9.5 完整门禁：
  ```bash
  make go-check
  ```
- [x] 9.6 ⚠️ 自查：本任务新增的代码**不得引入新的 `_ = fn()`**（`quality-guidelines.md` §3）。用 `grep -n "_ = " <改动文件>` 核对。
- [x] 9.7 逐条勾选 PRD §4 的 AC1–AC8，并**如实标注**：AC2 是降级版（不要求连接成功）；broker 宿主与热更新实现**未完成**。

---

## 交付物清单

| 类型 | 文件 |
| --- | --- |
| 契约文档 | `.trellis/spec/native/algo-plugin-contract.md` |
| 路由/中间件 | `internal/app/router.go`、`internal/app/app.go`、`internal/httpmiddleware/http.go` |
| 算法模块 | `internal/algorithm/{handler,uploader,module,process,verifier,supervisor,recovery,locale}.go` |
| 人员模块 | `internal/person/service.go`（仅注释） |
| 测试 | `internal/algorithm/*_test.go`、`internal/app/router_test.go` |
| 前序任务修订 | `10-12-algo-package-lifecycle/{prd,design}.md` |

## 明确不做

PRD §5 的全部范围外项，特别是：**broker 宿主实现**、**帧发布通路**、**底库热更新实现**、Swagger 注解、未注册错误码、422 绑定校验、死代码清理。

⚠️ 收尾时**不得**声称「算法插件端到端可用」——视频帧通路仍不存在。
