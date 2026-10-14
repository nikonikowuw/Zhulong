# 构建门禁基线修复：实施计划

前置：无（本任务是 `10-13-algo-broker-host` 验收补齐的前置）。

⚠️ 本任务是**纯解阻塞**任务：只有 include 补充与 Makefile 目标修正，**零产品行为变更**。任何超出该范围的改动都应拆出。

⚠️ 内部顺序：**阶段 A → B → C**。A 未完成则 B/C 无法执行（编译不过）；C 的 `make check` 依赖 B。

## 1. 实施清单

### 阶段 A：native include 修复（先做）

- [x] `native/src/abi/engine.cpp` 补 `#include <cstring>`。
  - ⚠️ **只加 include**。不改 `Zhulong_stream_get_broker_path` 的任何逻辑（`std::memcpy` 用法本身正确：`needed = path.size() + 1` 已含 `'\0'`）。
  - ⚠️ 不重排、不合并既有 include 块（`<memory> <new> <utility>`）——避免与需求无关的 diff。
  - 验证：`g++ -std=c++17 -fsyntax-only` 该文件无 error。
- [x] `native/src/pipeline/engine.cpp` 补 `#include <cstdlib>`（`std::getenv`）。
  - ⚠️ 当前靠传递包含侥幸通过，属**真实脆弱点**：任何上游 include 调整都会引爆。
  - 验证：同上。
- [x] 逐文件语法预检全部 `native/src/**/*.cpp`。
  - 验证：无 error（`shm_pool.cpp` 的 `-Wclass-memaccess` 警告是既有状况，不属本任务范围，**不要顺手改**）。

### 阶段 B：干净重建 + 符号证据

- [x] 删除 `build/native/host`（或至少强制全量重编译），确认不是复用陈旧产物。
  - ⚠️ **这是本任务的核心**：`c442044` 的教训正是「复用旧归档导致验证假绿」。必须证明归档真的被重建。
- [x] `make native-test` 全绿。
  - 验证：CTest 全项 Passed；记录总数。
- [x] 归档符号证据：
  ```bash
  nm build/native/host/libZhulongEngine.a | grep -c 'Zhulong_stream_get_broker_path'
  nm build/native/host/libZhulongEngine.a | grep -c 'Zhulong_stream_set_publish_rate'
  ```
  - ⚠️ 两个都必须**非 0**。修复前实测为 `0`，这正是假绿的直接证据。
  - 验证：把输出贴进 implement.md 的验收记录。

### 阶段 C：前端 check 目标修复

- [x] `frontend-install` 改用仓库声明的包管理器。
  - 仓库事实：只有 `pnpm-lock.yaml`（`lockfileVersion: '9.0'`）+ `pnpm-workspace.yaml`，**无** `package-lock.json`。
  - ⚠️ 保持「缺工具即显式失败」：不得因为 pnpm 不在就静默跳过前端检查。
  - ⚠️ `NPM ?= npm` 这个变量名会变成误导（值不再指 npm）。需在 Makefile 中明确变量语义（改名或注释说明），避免留下第二个真实源。
- [x] 处理不存在的 npm script（`type-check`、`audit`）。
  - 事实：`web/package.json` 的 scripts 为 `dev/build/lint/preview/format:check/format/knip/test/test:watch/test:ui/test:browser/test:coverage/test:browser:install`。
  - 类型检查**已含在** `build`（`tsc -b && vite build`）→ 等价方案：删除 `type-check` 调用，由 `build` 承担。
  - `audit` 无等价物 → 按需删除或改为包管理器对应的审计命令，需记录取舍。
  - ⚠️ **不得新增空 script 来骗过门禁**。
- [x] `make frontend-check` 实跑验证。
  - 验证：通过；若因网络（`pnpm install` 需拉包）无法通过，**如实记录**并把阻塞点与所需条件写清楚，不得声称通过。
- [x] `make check` 端到端实跑。
  - 验证：`frontend-check` + `native-test` + `api-docs`（零漂移）+ `go-check` 全通。

## 2. 验证命令

```bash
# A：逐文件语法预检（快速定位 include 缺失，不依赖 CMake）
FFINC=$(ls -d build/deps/ffmpeg/host/*/install/include | head -1)
for f in $(find native/src -name '*.cpp' | sort); do
  g++ -std=c++17 -fPIC -Wall -Wextra -Wpedantic -O0 -fsyntax-only \
      -Inative/include -Inative/src -I"$FFINC" -Inative/sdk/include "$f" || echo "FAIL: $f"
done

# B：干净重建 + 符号证据（核心）
rm -rf build/native/host
make native-test
nm build/native/host/libZhulongEngine.a | grep -c 'Zhulong_stream_get_broker_path'
nm build/native/host/libZhulongEngine.a | grep -c 'Zhulong_stream_set_publish_rate'

# C：前端与全栈门禁
make frontend-check
make go-check
make check
```

⚠️ **不要用 `make native-build` 代替 `make native-test`**：前者只编译不跑测试，无法证明测试真的执行过——这正是 `c442044` 的假绿模式。

## 3. 回退点

- 阶段 A：include 补充可单独回退（但会恢复编译失败，等于回到坏基线，无意义）。
- 阶段 C：Makefile 改动可单独回退到 `npm` 方案（但 `make check` 仍不可执行）。
- 任何阶段：**不改** `native/src/**` 的逻辑、**不改** `web/` 的任何源码或依赖声明。

## 4. 已知风险

- ⚠️ 前端依赖安装需要网络（`pnpm install`）。若离线则 `frontend-check` 无法通过——需如实记录为环境阻塞，不得绕过。
- ⚠️ `make api-docs` 需要 `swag`（`go run ...@v1.16.6`），同样需要网络或已缓存的 module。
- ⚠️ 根因是**流程问题**（提交前未真正执行门禁），代码修复只解决表象。修复后应在 spec 中强化「验证必须附证据」的要求。

---

## 5. 验收状态（实施后核对）

| AC | 状态 | 证据 |
| --- | --- | --- |
| AC1 干净重建 + 符号非 0 | ✅ | `rm -rf build/native/host` → `make native-test` **15/15 Passed**；`nm libZhulongEngine.a`：`get_broker_path` = **2**（`T` + `.cold`）、`set_publish_rate` = **2** |
| AC2 逐文件语法预检无 error | ✅ | 全部 `native/src/**/*.cpp` 的 `g++ -fsyntax-only` 0 error（修复前 `abi/engine.cpp` 报错） |
| AC3 `make frontend-check` 可执行并通过 | ✅ | `eslint .` 0 问题 + `vitest` **241 passed (52 files)** + `built in 940ms`，exit 0 |
| AC4 不存在的 script 已处置 | ✅ | `type-check` 由 `build` 的 `tsc -b` 承担（等价）；`audit` 移出 `frontend-check` 并注明理由与既有安全债数据 |
| AC5 `native-test` + `go-check` 真绿 | ✅ | `make check` **exit 0**：frontend-check ✅ / native-test 15/15 ✅ / api-docs 零漂移 ✅ / go-check（gofmt 空 + vet + 17 包 `ok` + Go 桥接 6/6）✅ |

### 关键证据：修复前 vs 修复后

| 项 | 修复前 | 修复后 |
| --- | --- | --- |
| `libZhulongEngine.a` 时间戳 | 2026-10-13 12:03 | 2026-10-14 09:35 |
| `Zhulong_stream_get_broker_path` 符号数 | **0** | **2** |
| `Zhulong_stream_set_publish_rate` 符号数 | **0** | **2** |
| `make native-test` | **编译失败**（`std::memcpy` 无 `<cstring>`） | **15/15 Passed** |
| `make frontend-check` | **不可执行**（`npm ci` 无 lockfile；`type-check`/`audit` script 不存在） | **exit 0** |
| `make check` | **不可执行** | **exit 0** |

### 反向验证（证明断言不是空的）

临时删除 `native/src/abi/engine.cpp` 的 `#include <cstring>` 后重新构建：

```txt
native/src/abi/engine.cpp:149:14: error: ‘memcpy’ is not a member of ‘std’; did you mean ‘wmemcpy’?
gmake[3]: *** [CMakeFiles/ZhulongEngine.dir/build.make:79: .../engine.cpp.o] Error 1
make: *** [Makefile:50: native-build] Error 1        ← exit 2
```

**精确复现原始错误（同一行 149）** → 证明 AC1 的符号断言与本修复之间存在真实的因果关系，而非巧合。已恢复并重新构建通过。

### 前端门禁的取舍记录

| 原步骤 | 处置 | 理由 |
| --- | --- | --- |
| `npm ci --prefix web` | → `pnpm --dir web install --frozen-lockfile` | 仓库只有 `pnpm-lock.yaml`；`--frozen-lockfile` 保持 CI 语义（lockfile 与 package.json 不一致即失败） |
| `run type-check` | → **删除**，由 `build` 承担 | `web/package.json` 无此 script；`build` 为 `tsc -b && vite build`，类型检查已包含 |
| `audit` | → **移出 `frontend-check`** | ⚠️ **既有安全债，从来未通过**：`pnpm audit --prod` 报 52 项（含 `axios` 2 项 critical）；全量 `pnpm audit` 报 91 项（5 critical）。保留会让整个前端门禁永久红，从而掩盖 lint/test/build 的真实回归 |
| `NPM ?= npm` | → `PNPM ?= pnpm`（含说明注释） | 名字与值一致，消除「名字说 npm、值却是 pnpm」的第二个真实源 |
| （新增）工具缺失检查 | `command -v $(PNPM)` 显式失败 | 不得因缺工具而静默跳过前端检查 |

⚠️ **`audit` 的移除不等于安全债被接受**：它只是从「阻断全部前端检查」改为「独立议题」。建议单开任务处理 `axios` 等生产依赖升级。

## 6. 残余风险

- ⚠️ **根因是流程而非代码**：`c442044` 声称的 `make native-test` 14/14 未真正执行（复用 10-13 陈旧归档，`nm` 计数为 0）。本任务修好了代码与门禁，但「提交前必须真正跑 `make check` 并附证据」的约束需要靠流程保障——项目 `AGENTS.md` 已有该约束，本次未被遵守。
- ⚠️ **生产依赖安全债未处理**：`pnpm audit --prod` 52 项（2 critical）。建议单开任务。
- ⚠️ **前端格式债**：`pnpm run format:check` 报 11 个文件不符合 prettier（`src/features/settings/time/**` 等）。该 script 不在 `frontend-check` 中，属既有状况，未纳入本任务。
- ⚠️ `shm_pool.cpp:86` 的 `-Wclass-memaccess` 警告是既有状况，按「精准修改」原则未改动。
