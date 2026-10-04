# 原生处理引擎规范总览 (C++ / CGO)

> C++ 音视频流水线（Node Pipeline）、CGO 跨语言边界、内存所有权与安全停机。

---

## 1. 规范索引

| 规范指南 | 核心内容 |
| --- | --- |
| [目录架构](./directory-structure.md) | C ABI 公开头文件、私有节点目录、适配层边界及单向依赖 |
| [跨语言命名规范](../naming-guidelines.md) | C/C++ 文件、类型、函数、变量及 C ABI 符号 |
| [CGO 交互与内存契约](./cgo-contract.md) | 内存所有权、不透明句柄、Go 指针规则、异常隔离与线程绑定 |
| [节点流水线设计](./pipeline-guidelines.md) | 节点职责、有界队列背压、零拷贝缓冲传递、5 步优雅停机 |

---

## 2. 开发前检查清单 (Pre-Development Checklist)

- [ ] **ABI 纯净性**：公开头文件 `include/Zhulong/engine.h` 是否纯 C 结构，无任何 C++ class/STL/模板？
- [ ] **命名自然清晰**：文件、C ABI 符号、C++ 类型、函数和变量是否自然且见名知意？
- [ ] **异常防爆门**：C ABI 导出函数是否全量 `try-catch (...)` 拦截，杜绝异常穿越 CGO 导致崩溃？
- [ ] **内存所有权明确**：谁分配谁释放？`C.CString` 是否配对 `defer C.free`？
- [ ] **Go 指针合规**：传给 C 的指针内存是否不包含 Go 指针？C 侧是否未长期持有 Go 指针？
- [ ] **队列有界背压**：节点队列是否有硬性容量上限？AI 推理队列是否采用了丢弃旧帧策略？
- [ ] **停机汇合 (Join)**：销毁引擎前是否已停止生产者、唤醒等待并等待所有工作线程 `join()`？

---

## 3. 质量检验基线

```bash
# 待构建系统建立后配置：开启 ASan 与 UBSan 门禁
# -fsanitize=address -fsanitize=undefined
```

*绿地基线准则：当前尚未选定构建系统（CMake/Makefile）与硬件 SDK，规范为架构约束，测试门禁将在构建系统引入后落地。*
