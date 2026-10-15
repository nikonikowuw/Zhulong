# Implementation Plan: 精简与优化 .trellis/spec 工程规范

## 1. 执行步骤清单

### 步骤 1：拆分与精简 Native 硬件加速规范 (方案 A)
- [x] 1.1 从 `native/algo-plugin-contract.md` 提取 RKNN 和 RGA 硬件后端章节，创建新文件 `.trellis/spec/native/hardware-backends.md`。
- [x] 1.2 清理 `hardware-backends.md` 中的开发期任务编号与交付散文，保持技术数据与踩坑约束精确完整。
- [x] 1.3 精简 `native/algo-plugin-contract.md`，删除重复的 `manifest.json` 完整定义，引用 `backend/algorithm-guidelines.md`；添加指向 `hardware-backends.md` 的链接。
- [x] 1.4 更新 `native/index.md` 导航表格，补充 `hardware-backends.md`。
- [x] 1.5 **验证**：检查 native 规范行数变化与文件链接有效性。

### 步骤 2：精简后端算法包与 HTTP 规范
- [x] 2.1 精简 `backend/algorithm-guidelines.md`，删除重复的 C++ SDK 代码实现、C ABI 结构体与退出码表，改为链接至 `native/algo-plugin-contract.md`。
- [x] 2.2 精简 `backend/http-api-guidelines.md`，移除与 `backend/error-handling.md` 重复的响应信封定义与错误 JSON 示例，保留链接引用。
- [x] 2.3 精简 `backend/storage-guidelines.md` §1.4，删除关于旧绝对路径废弃的辩解散文，直接陈述文件直传契约。
- [x] 2.4 **验证**：确保后端规范各章节无信息缺失，语法通顺。

### 步骤 3：中文化与紧凑化 Native Ingestion 合同
- [x] 3.1 将 `native/ingestion-contract.md` 翻译为紧凑的中文工程规范。
- [x] 3.2 剔除实验调试日志式叙述，提炼为清晰的前置约束、超时逻辑、线程所有权与析构保证。
- [x] 3.3 **验证**：核对 C ABI 签名与 Go 侧超时解耦原则完整无误。

### 步骤 4：全局历史任务代号清理与全局链接校验
- [x] 4.1 全仓扫描 `.trellis/spec/`，清理残留的任务代号（如 `任务 10-13-*`、`10-14-*`）以及不必要的开发历史描述。
- [x] 4.2 编写单行脚本验证全仓 `.trellis/spec/**/*.md` 中所有 Markdown 相对链接，确保无失效死链。
- [x] 4.3 统计最终总行数，验证是否满足缩减目标。

---

## 2. 验证命令与结果

```bash
# 1. 验证没有遗留的任务编号
grep -rn "任务 10-" .trellis/spec/ || echo "No task slugs found! [PASS]"
# 结果：No task slugs found! [PASS]

# 2. 验证 Markdown 相对链接有效性
python3 -c '
import os, re, sys
errors = []
for root, dirs, files in os.walk(".trellis/spec"):
    for f in files:
        if f.endswith(".md"):
            path = os.path.join(root, f)
            with open(path) as fp:
                content = fp.read()
            for m in re.finditer(r"\[.*?\]\((?!http|mailto)(.*?)\)", content):
                target = m.group(1).split("#")[0]
                if not target: continue
                target_path = os.path.normpath(os.path.join(root, target))
                if not os.path.exists(target_path):
                    errors.append(f"{path}: broken link -> {target}")
if errors:
    print("\n".join(errors))
    sys.exit(1)
print("All internal links verified! [PASS]")
'
# 结果：All internal links verified! [PASS]

# 3. 统计总行数变化
# 原总行数：3,847 行
# 现总行数：3,360 行 (减少 487 行冗余，同时 algo-plugin-contract 从 39.8KB 缩减至 19.5KB，成功解除上下文截断告警)
```
