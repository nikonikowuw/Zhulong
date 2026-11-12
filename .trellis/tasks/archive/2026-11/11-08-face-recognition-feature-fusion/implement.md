# 实施计划：人脸识别跨帧特征融合与同轨迹升级落库

## 1. 有序实施清单

1. **阶段一：ONNX target 配置、融合核心与流水线接线**
   - 修改 `packages/face_recognition/onnx/config.schema.json`：新增 `enable_face_feature_fusion`（默认 `true`）、`fusion_similarity_threshold`（`[0.45, 0.95]`，默认 `0.62`）、`fusion_ttl_seconds`（`[0.5, 30.0]`，默认 `3.0`）。
   - 修改 `packages/face_recognition/onnx/src/common/match_config.hpp`：在 `MatchParams` 与 `resolve_match_params` 中同步上述 3 个字段的常量、边界校验与扁平 JSON 解析。
   - 新增 `packages/face_recognition/onnx/src/common/face_feature_fusion.hpp`：实现 header-only 的 `StreamFeatureFusionPool` 与 `TaskFeatureFusionManager`（整帧一对一贪心匹配、融合特征+锚点特征双重余弦门禁、带 `kMaxAccumulatedWeight = 6.0f` 软上限的质量加权累加与 L2 重归一化、TTL 淘汰、32 条状态上限与 256 路桶上限）。
   - 修改 `packages/face_recognition/onnx/src/pipeline/face_recognition.{hpp,cpp}`：在 `FaceRecognitionPlugin` 持有 `TaskFeatureFusionManager`，将 `detect_scene_structure` 的人脸处理拆分为「逐脸质量/对齐/提特征 → 整帧跨帧融合 → 1:N 比对与结果组装」，透传 `is_fused`。
   - 扩充 `packages/face_recognition/onnx/tests/aligner_matcher_test.cpp`：新增参数解析测试与跨帧融合数学/防漂移/互斥/TTL/容量/多路隔离测试。
2. **阶段二：同步 RK3568 target 与算法包文档**
   - 将 `config.schema.json`、`match_config.hpp`、`face_feature_fusion.hpp`、`aligner_matcher_test.cpp` 同步至 `packages/face_recognition/rk3568/`，并按 `rk3568` 的细微行号/扩展名差异同步 `rk3568/src/pipeline/face_recognition.{hpp,cpp}`。
   - 更新 `packages/face_recognition/README.md`，补充跨帧特征融合流水线阶段与新增 schema 参数说明。
   - 运行 `diff -u` 与 `make pkg-drift-check` 验证双 target 零漂移。
3. **阶段三：Go 宿主 `internal/record` 同轨迹冷却升级与规范同步**
   - 修改 `internal/record/cooldown.go`：新增 `AllowAllAndStamp(checkKeys, stampOnlyKeys []string, now time.Time) bool`，令 `AllowAll` 复用该实现。
   - 修改 `internal/record/cooldown_keys.go` 与 `internal/record/service.go`：对 `trackID > 0` 的未匹配人脸返回 `rec:{camera}:trk:{id}:unmatched`，对已匹配人脸保持 `rec:{camera}:trk:{id}` + `rec:{camera}:person:{person_id}` 并在原子通过时顺带打戳 `rec:{camera}:trk:{id}:unmatched`。
   - 同步更新 `.trellis/spec/backend/record-guidelines.md` §1 去重键说明。
   - 扩充 `internal/record/track_dedup_test.go`：新增未匹配跟踪键契约断言，以及同一 `track_id` 在冷却期内 `unmatched → matched`、`unmatched → unmatched`、`matched → matched`、`matched → unmatched` 与跨轨迹拒绝不污染的端到端测试。
4. **阶段四：全链路验证与门禁检查**
   - 依次执行包单测、包端到端推理自测、Native 编译与 CTest、Go `-race` 测试与 `make go-check`。

## 2. 验证命令

```bash
# 1. ONNX 包纯数据与融合数学单测 + 端到端实图自测
make -C packages/face_recognition/onnx unit-test SDK_PREFIX=$(pwd)/build/sdk-verify/onnx ONNXRUNTIME_ROOT=$(pwd)/native/.cache/onnxruntime-linux-x64-1.24.4
make -C packages/face_recognition/onnx test SDK_PREFIX=$(pwd)/build/sdk-verify/onnx ONNXRUNTIME_ROOT=$(pwd)/native/.cache/onnxruntime-linux-x64-1.24.4

# 2. 双 target schema 与源码零漂移门禁
diff -u packages/face_recognition/onnx/config.schema.json packages/face_recognition/rk3568/config.schema.json
make pkg-drift-check

# 3. Native 引擎与插件 C ABI / ONNX 集成测试
make native-build
make native-test

# 4. Go 感知记录与宿主全量静态/竞态门禁（遵循 AGENTS.md：严禁裸跑 go test）
python3 native/scripts/build.py go test -race ./internal/record/...
make go-check
```

## 3. 风险文件与回滚点

- `packages/face_recognition/{onnx,rk3568}/src/pipeline/face_recognition.cpp`：`onnx` 与 `rk3568` 存在 3 处合法 target 差异（如 `.onnx` vs `.rknn` 模型后缀及开发期候选路径），同步时必须按块修改并跑 `make pkg-drift-check`，禁止整文件盲拷覆盖 target 常量。
- `internal/record/cooldown_keys.go` 与 `internal/record/track_dedup_test.go`：已匹配人脸键名必须严格保持 `rec:{camera}:trk:{id}`，仅未匹配跟踪人脸使用 `:unmatched` 后缀，确保既有两级抑制行为零退化。
- 注意保留工作区中上一任务 `11-06-face-recognition-cctv-optimize` 尚未提交的改动及环境自有未跟踪文件（`.agent/`、`.mcp.json`、`CLAUDE.md`）。

## 4. `task.py start` 前置检查

- [x] `prd.md` 已完成收敛检查，无阻塞性开放问题、无重复段落。
- [x] `design.md` 与 `implement.md` 已就绪。
- [x] `implement.jsonl` 与 `check.jsonl` 已填入真实规范条目。
- [ ] 用户明确批准最终规划摘要后再执行 `task.py start` 与代码修改。
