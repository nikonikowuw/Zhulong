# 技术设计：实现人脸识别算法包 (10-14-face-algo-package)

## 1. 架构目标与定位

交付一个**符合 Zhulong 平台规范、可独立分发与安装、支持 1:N 检索的人脸识别算法包**：
1. **任务形态与输出标准**：遵循 `task_type: "person_structure"` 规范，输出 `zhulong_person_item`（退化单人脸语义：`track_id = 0`, `body_box = face_box`, `body_score = 1.0f`, 挂载 `zhulong_face_item`）；
2. **算法计算管线**：执行 `detect`（人脸定位与 5 点关键点）➔ `align`（ArcFace 5 点对齐）➔ `embed`（512 维 L2 归一化特征提取）➔ `1:N 检索`（基于当前底库快照的余弦相似度比对与 Top-K 候选排序）；
3. **跨层底库共享桥接**：Runner 进程在接收到 `MSG_GALLERY_SYNC` / `MSG_GALLERY_UPSERT` / `MSG_GALLERY_DELETE` 时，更新进程内全局 `GalleryRegistry`，插件在推理时通过零锁或极低开销只读快照 `snapshot()` 完成 1:N 匹配，动态响应底库增删；
4. **验包门禁保证**：自带 `sample.jpg`，通过 `zhulong_algo_runner --verify-plugin` 黑盒准入测试，以及 Go 宿主 `InstallAlgorithm` 校验；
5. **坐标与数值铁律**：所有输出坐标严格归一化至 $[0.0, 1.0]$，杜绝越界。

```txt
┌───────────────────────────────── 算法 Runner 进程 ─────────────────────────────────┐
│                                                                                     │
│  UnixSocketClient 接收下行控制消息                                                    │
│    └─ MSG_GALLERY_SYNC / UPSERT / DELETE ──→ GalleryRegistry::instance().update()   │
│                                                   │                                 │
│  帧流驱动 (api.process)                           │ 只读快照 snapshot()              │
│    │                                              ▼                                 │
│    ▼                                     ┌──────────────────┐                       │
│  FaceRecognitionPlugin                   │ FaceTemplate[]   │                       │
│    ├─ 1. Detect (BBox & 5-Landmarks)     │ - face_id        │                       │
│    ├─ 2. Align (112x112 ArcFace 对齐)    │ - person_id      │                       │
│    ├─ 3. Embed (512-d 特征向量计算)      │ - person_name    │                       │
│    └─ 4. Match (1:N 余弦比对与 Top-K) ◄──┤ - features (512) │                       │
│           │                              └──────────────────┘                       │
│           ▼                                                                         │
│  返回 zhulong_person_item                                                           │
│    (matched, similarity, person_id, person_name, candidates[])                      │
│                                                                                     │
└──────────────────────────────────────┬──────────────────────────────────────────────┘
                                       │ MSG_RESULT (JSON)
                                       ▼
                              Go 宿主 (Egress 消费端)
```

---

## 2. 核心模块与接口设计

### 2.1 运行时底库注册表 (`GalleryRegistry`)

在 `libZhulongAlgoRuntime.so` 或公共头中定义跨动态库共享的底库注册表接口：

```cpp
namespace zhulong::runtime {

class GalleryRegistry {
public:
    static GalleryRegistry& instance();

    void reset(std::vector<FaceTemplate> items);
    void upsert(FaceTemplate item);
    void remove(const std::string& face_id, const std::string& person_id);

    std::shared_ptr<const std::vector<FaceTemplate>> snapshot() const;
    size_t size() const;
};

} // namespace zhulong::runtime
```

- **生命周期**：Runner 进程与插件动态库均链接 `libZhulongAlgoRuntime.so`，共享单例上下文；
- **冷启动与增量**：Runner 在解析到 `MSG_GALLERY_SYNC` 时调用 `reset`；解析到 `MSG_GALLERY_UPSERT` 时调用 `upsert`；解析到 `MSG_GALLERY_DELETE` 时调用 `remove`；
- **验包安全性**：在单次验包模式（`--verify-plugin`）下，底库为空，插件执行人脸检测与特征提取后，1:N 匹配安全返回 `matched = false`，不产生崩溃或非法访问。

### 2.2 人脸识别插件 (`FaceRecognitionPlugin`)

遵循 `zhulong/algo.hpp` 现代 C++17 SDK 门面：

```cpp
class FaceRecognitionPlugin {
public:
    zhulong_status_t init(const std::string& config_json);
    zhulong::Session create_session(uint32_t worker_id);
    std::vector<zhulong::PersonItem> detect_persons(zhulong::Session& session, const zhulong::Image& image);

private:
    float threshold_{0.40f}; // 余弦相似度判定阈值
    uint32_t top_k_{3};      // 候选人列表保留数
};

ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN(FaceRecognitionPlugin, "FaceRecognition", "1.0.0")
```

#### 推理执行步骤 (`detect_persons`)
1. **人脸检测**：
   - 输入：只读图像帧 `zhulong::Image`；
   - 提取画面中的人脸候选框 $[x_1, y_1, x_2, y_2] \in [0.0, 1.0]$ 及 5 点关键点；
   - 若画面全黑或无有效面孔，返回空列表 `{}`。
2. **人脸对齐与标准化**：
   - 依据 5 点关键点与标准 ArcFace 5 点参考模板，计算仿射变换；
   - 裁剪对齐至 $112 \times 112$ 空间，进行 RGB 像素标准化 $(x - 127.5) / 127.5$。
3. **特征向量提取**：
   - 提取 512 维特征向量，并执行 L2 单位归一化 $\|\mathbf{f}\|_2 = 1.0$；
   - 保证与 `Zhulong_engine_extract_face_feature` 产生同构特征空间。
4. **1:N 检索与 Top-K 排序**：
   - 从 `GalleryRegistry::instance().snapshot()` 获取当前底库模板集合；
   - 遍历底库模板计算余弦相似度：$S_i = \mathbf{f} \cdot \mathbf{g}_i$；
   - 降序排序，取最高 Top-K 生成候选人列表 `candidates`；
   - 若 Top-1 相似度 $S_{\max} \ge \text{threshold}$：
     - `matched = true`
     - `similarity = S_max`
     - `person_id = best.person_id`（业务键原值）
     - `person_name = best.person_name`
   - 否则：
     - `matched = false`
     - `similarity = S_max`
     - `person_id = ""`
     - `person_name = ""`
5. **结构化组装**：
   - 组装 `zhulong::PersonItem`，返回给 SDK 门面；
   - SDK 门面使用单块连续 Arena 内存打包成 `zhulong_inference_result`（`result_type = 3`）。

---

## 3. 算法包规范与源码工程拓扑

### 3.1 源码工程目录结构 (`packages/face_recognition/`)

业务流水线、数学对齐、1:N 检索使用统一源码，芯片专属资产（模型文件与 manifest）按 `targets/<chip>` 彻底隔离：

```txt
packages/face_recognition/
├── CMakeLists.txt                 # 插件编译工程（依赖 ZhulongAlgoRuntime）
├── src/                           # 【跨平台通用流水线源码】
│   ├── face_recognition.cpp       # 插件入口与 SDK 门面
│   ├── aligner.cpp / .hpp         # ArcFace 5点几何相似变换（Umeyama 算法）
│   └── matcher.cpp / .hpp         # 1:N 余弦距离检索与 Top-K 堆排序
│
├── targets/                       # ★ 【按芯片与平台严格拆分】
│   ├── cpu/                       # x86_64 开发机 / 容器 / 通用服务器
│   │   ├── manifest.json          # arch: "x86_64", target: "cpu", compute_cost: 25, memory_mb: 128
│   │   └── models/                # det_500m.onnx, facelivtv2-l.onnx
│   │
│   ├── rk3568/                    # 瑞芯微 RK3568 (Debian 11 arm64 / librknnrt 2.3.2)
│   │   ├── manifest.json          # arch: "aarch64", target: "rk3568", compute_cost: 35, memory_mb: 64
│   │   └── models/                # det_500m.rknn, facelivtv2-l_fp16.rknn (必须 fp16)
│   │
│   └── rk3588/                    # 瑞芯微 RK3588 (预留)
│       └── manifest.json
│
├── sample.jpg                     # 跨芯片通用的标准自测样本图
├── config.schema.json             # 跨芯片通用的超参数定义 (如 threshold)
└── scripts/
    └── build_package.py           # 自动化打包脚本（指定 target 自动组装）
```

### 3.2 交付产物拓扑（单一目标薄包）

最终输出至板端的交付包保持严格扁平：

```txt
face_recognition-<arch>-<target>.tar.gz
├── manifest.json       # 来自对应 targets/<target>/manifest.json
├── plugin.so           # 对应架构交叉编译输出的纯 C ABI 动态链接库 (0755)
├── sample.jpg          # 标准自测图
├── config.schema.json  # 超参数配置（如阈值 threshold）
└── models/             # 仅含当前目标芯片的权重模型
```

### 3.3 `targets/cpu/manifest.json` 元数据声明

```json
{
  "id": "face_recognition",
  "name": "人脸识别算法",
  "version": "1.0.0",
  "description": "基于 512 维特征向量与毫秒级 1:N 检索的高性能人脸识别算法包",
  "task_type": "person_structure",
  "category": "safety",
  "platform": {
    "arch": "x86_64",
    "target": "cpu"
  },
  "resources": {
    "compute_cost": 25,
    "memory_mb": 128
  }
}
```

### 3.4 `config.schema.json` 参数规范

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "threshold": {
      "type": "number",
      "description": "人脸比对余弦相似度命中阈值",
      "minimum": 0.0,
      "maximum": 1.0,
      "default": 0.40
    },
    "top_k": {
      "type": "integer",
      "description": "候选人返回上限",
      "minimum": 1,
      "maximum": 10,
      "default": 3
    }
  }
}
```

---

## 4. 关键设计细节与防坑指南

1. **坐标系归一化强检**：
   - 坐标必须满足 $0.0 \le x_1 < x_2 \le 1.0$，$0.0 \le y_1 < y_2 \le 1.0$；
   - 5 点关键点必须满足 $0.0 \le x_k, y_k \le 1.0$；
   - 任何超出范围都会导致 `verifier.go` 验包直接拒绝。
2. **纯人脸退化语义**：
   - 纯人脸算法不包含独立人体检测网络，因此采用 ABI 规约的退化语义：
   - `body_box = face_box`；
   - `body_score = 1.0f`；
   - `track_id = 0`；
   - `faces` 数组包含 1 张人脸。
3. **身份标识一致性**：
   - `person_id` 严格取自 `FaceTemplate.person_id`（对应 Go 侧 `persons.person_id` 业务键），禁止混入行主键。
4. **内存安全与 RAII**：
   - 所有输出结果均通过 `zhulong::detail::PersonStructureResultAdapter` 一次性分配连续 Arena 内存；
   - 结果内存销毁必须走同一插件的 `result_release`，杜绝内存碎片与嵌套泄漏。

---

## 5. 验证与测试策略

1. **单元测试与 ABI 验证**：
   - 在 `native/tests/` 中编写 `face_recognition_plugin_test.cpp`，覆盖：
     - 空底库场景：验包正常返回 `matched = false`；
     - 有底库匹配场景：正确命中已登记人脸，返回 `matched = true` 与正确 `person_id`；
     - 异人不同照场景：低于阈值，正确返回 `matched = false`。
2. **验包 CLI 验证**：
   - 运行 `zhulong_algo_runner --verify-plugin plugin.so --sample sample.jpg`；
   - 断言返回退出码 0，且输出包含 `result_type: 3` 与 `count: 1`。
3. **Go 宿主集成测试**：
   - 打包 `face_recognition.tar.gz`；
   - 运行 `verifier.VerifyDirectory` 与 `service.InstallAlgorithm`，验证安装成功且入库元数据准确。
4. **全量门禁检查**：
   - `make native-test` 全部通过；
   - `make go-check` 全部通过；
   - `make check` 全栈全绿。
