# 感知记录多维批量与多选导出 — 技术设计 (Design)

## 1. 架构原则与算力拓扑

```
               [ 边缘宿主 Zhulong (RK3568 / RK3588) ]
                                 │
              ┌──────────────────┴──────────────────┐
              ▼                                     ▼
      SQLite (WAL 只读游标)             媒体库 /media/content/*
    JSON 记录快速分页查询               静态 JPEG 图片极速直传 (零转码)
              │                                     │
              └──────────────────┬──────────────────┘
                                 │ HTTP / JSON / Static Streams
                                 ▼
                     [ 客户端浏览器 Chrome / Edge ]
                                 │
              ┌──────────────────┴──────────────────┐
              ▼                                     ▼
      离屏 Canvas 渲染管线                  流式 ZIP 归档器 (fflate)
  - 自动物理分辨率坐标映射                - 受控并发队列 (Concurrency=3)
  - 动态自适应线宽与标签胶囊            - 单图即做即入即释放
  - 像素级半透明目标框与文字            - 浏览器 JS 堆内存始终 < 50MB
              │                                     │
              └──────────────────┬──────────────────┘
                                 ▼
                     单文件 ZIP / CSV 一键直接下载
```

### 核心设计收益：
1. **边缘计算零压力**：宿主坚决不引入图像合成 CGO/OpenCV，不消耗板卡 CPU/GPU 资源，视频流硬解与 AI 推理完全不受干扰；
2. **端侧内存零膨胀**：采用流式即收即放，防止大图 Blob 聚集，杜绝浏览器 Tab OOM；
3. **视觉标准高度统一**：前端 Canvas 绘制的边框与标签风格与前端现有的 `BoundingBoxCanvas`、`RecordDetailDialog` 完全一致。

---

## 2. 核心模块与文件规划

```
web/src/features/records/
├── components/
│   ├── export/
│   │   ├── records-export-dialog.tsx       # 导出配置对话框（维度选择、格式选择、画框开关、进度条）
│   │   ├── records-export-dialog.test.tsx  # 导出对话框单元测试
│   │   └── records-selection-bar.tsx       # 底部多选浮动操作条（已选 N 项、导出、清空）
│   ├── views/                              # 改造：添加 Checkbox 行选择与卡片选择
│   │   ├── recognitions-view.tsx
│   │   ├── alarms-view.tsx
│   │   └── captures-view.tsx
│   └── common/
│       └── selectable-card-wrapper.tsx     # 卡片模式复选框包裹层
├── hooks/
│   ├── use-records-selection.ts            # 多选状态管理 Hook (Set/Map, 支持跨页与快速清空)
│   └── use-records-selection.test.ts
├── lib/
│   ├── canvas-overlay-drawer.ts            # 核心：离屏 Canvas 检测框与标签烧录引擎
│   ├── canvas-overlay-drawer.test.ts
│   ├── export-csv.ts                       # CSV 转换与 UTF-8 BOM 序列化
│   ├── export-csv.test.ts
│   ├── export-zip-pipeline.ts              # 流式受控并发下载、离屏合成与 ZIP 压缩流水线
│   └── export-zip-pipeline.test.ts
└── locales/                                # zh-Hans / zh-Hant / en 国际化文本补充
```

---

## 3. 详细设计规范

### 3.1 离屏 Canvas 检测框烧录 (`canvas-overlay-drawer.ts`)

```typescript
export interface OverlayBurnOptions {
  /** 归一化目标框 [x1, y1, x2, y2] */
  primaryBox?: [number, number, number, number]
  primaryLabel?: string
  /** 归一化人体框 (识别记录特有) */
  secondaryBox?: [number, number, number, number]
  secondaryLabel?: string
  /** 颜色风格: emerald (人脸/命中), amber (告警), sky (抓拍), violet (陌生人) */
  palette?: 'emerald' | 'amber' | 'sky' | 'violet'
}

export async function burnOverlayOnImage(
  imageUrl: string,
  options: OverlayBurnOptions
): Promise<Blob>
```

- **分辨率自适应缩放**：
  - 边框宽度：`lineWidth = Math.max(2, Math.round(naturalWidth / 600))`；
  - 字体大小：`fontSize = Math.max(12, Math.round(naturalWidth / 80))`；
  - 标签内边距与圆角：根据 `fontSize` 等比自适应计算。
- **坐标换算**：
  - 若 `x2 <= 1.05 && y2 <= 1.05`，按归一化乘 `naturalWidth`/`naturalHeight`；若原坐标已是像素绝对值则直接使用并做边界 clamp 限制。
- **内存回收机制**：
  - 在完成 `toBlob()` 后，主动清空 canvas 上下文及解除 Image 引用，确保垃圾回收器立刻回收像素缓冲区。

### 3.2 CSV 序列化与 BOM 注入 (`export-csv.ts`)

- 遵循 RFC 4180 标准；
- 文件起始写入 `\uFEFF`（UTF-8 BOM），确保各版本 Microsoft Excel、WPS 正常识别字符集；
- 字段映射表：
  - **通用字段**：ID、抓拍时间 (`captured_at`)、摄像机名称、算法名称、算法版本、跟踪 ID (`track_id`)、PTS 帧戳；
  - **人脸识别**：是否命中、人员姓名、人员 ID、对外业务键 (`person_external_id`)、比对相似度；
  - **安全告警**：告警类型、告警置信度得分；
  - **目标/车辆抓拍**：目标类型、置信度得分、车牌/属性摘要。

### 3.3 受控流水线与 ZIP 打包 (`export-zip-pipeline.ts`)

- **依赖轻量化**：引入极轻量无依赖的 `fflate`（纯 JS、仅 ~8KB，支持流式 zip 压缩，无任何外部二进制或复杂 DOM 依赖）；
- **受控并发执行器**：
  - 并发度限制为 3~4，避免同时发出大量 HTTP GET 击垮边缘宿主或浏览器内存；
- **流水线步骤**：
  1. 收集目标记录列表（多选勾选记录直接传入，全量批量记录则按分页逐页拉取）；
  2. 生成各维度的 `.csv`，直接写入 zip 根目录；
  3. 若开启图片导出：
     - 并发拉取对应图片；
     - 若开启画框烧录：调用 `burnOverlayOnImage` 输出合成后图片；若未开启：直接获取原图字节；
     - 压入 zip 文件的 `images/{tab}/{record_id}.jpg` 目录；
     - 每处理完一张立即清理临时对象；
     - 触发实时进度通知回调 `onProgress(current, total, phase)`；
  4. 生成 zip 文件 Blob，通过浏览器动态 `<a>` 标签触发下载。

### 3.4 交互与状态设计

- **多选管理 (`use-records-selection.ts`)**：
  - 使用 `Map<string, RecordItem>` 存储，具备 `O(1)` 的增删改查；
  - 支持单选反选、全选当前页、跨页累计多选、一键清空；
  - 切换 Tab 时保留或者提供清空提示，保持体验可预期。
- **多选操作条 (`records-selection-bar.tsx`)**：
  - 悬浮于页面底端居中，具有毛玻璃质感与过渡动画；
  - 显示“已选择 X 条感知记录”；
  - 提供「导出所选项」、「清空选择」按钮。
- **导出对话框 (`records-export-dialog.tsx`)**：
  - 智能感知当前触发场景：
    - 多选触发：显示 `导出范围：已选中的 X 条记录`；
    - 全局触发：显示 `导出范围：当前筛选结果（包含自由勾选的 Tab 维度）`；
  - 勾选控制：
    - 数据格式：CSV 报表（默认开）、图片证据（可选）；
    - 画框控制：在图片中烧录检测框与研判标签（图片开启时可选，默认开启）；
  - 导出过程中，按钮变成取消按钮，并显示清晰阶段性进度条。

---

## 4. 边界与风险控制

| 风险项 | 风险等级 | 控制措施 |
|---|---|---|
| 大量图片导致前端内存泄漏 / OOM | **高** | 引入流式受控并发池（并发 3），图片处理完单张立即释放 Blob 和 Canvas，内存峰值恒定 < 50MB |
| 跨域污染导致 Canvas 导出图片失败 | **中** | 静态图片接口同源代理 (`/api/v1/media/content/*`)，设置 `crossOrigin = 'anonymous'` |
| 极端分辨率图片（如 4K）画框过细或文字过小 | **中** | 动态分辨率等比映射算法，根据画布实际物理像素自适应缩放边框厚度与字体大小 |
| 中文字符在 Excel 中显示为乱码 | **低** | CSV 文件头硬性注入 `\uFEFF` UTF-8 BOM |
