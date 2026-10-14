# 构建门禁基线修复：native 头文件与前端 check 目标

父任务：`10-10-algorithm-plugin-architecture`。本任务**不改变任何产品行为**，只修复两个使 `make check` 完全无法执行的既有缺陷，为 `10-13-algo-broker-host` 的验收补齐扫清前置。

## 1. 背景（2026-10-14 实测）

`10-13-algo-broker-host` 的实现已提交（`c442044`），但其「验收状态」表把 AC2/AC6/AC7/AC8/AC10/AC11 标为部分达成。本任务是在准备补齐这些 AC 时，发现**基线本身不可运行**。

### 1.1 缺陷 A：native 编译失败（阻塞级）

```txt
$ make native-test
/home/niko/dev/Go/Zhulong/native/src/abi/engine.cpp:149:14: error:
  ‘memcpy’ is not a member of ‘std’; did you mean ‘wmemcpy’?
gmake[3]: *** [CMakeFiles/ZhulongEngine.dir/src/abi/engine.cpp.o] Error 1
make: *** [Makefile:48: native-test] Error 1
```

`c442044` 新增的 `Zhulong_stream_get_broker_path` 使用 `std::memcpy`，但 `native/src/abi/engine.cpp` 只 include 了 `<memory> <new> <utility>`，**没有 `<cstring>`**。

同类问题一处：`native/src/pipeline/engine.cpp:23` 使用 `std::getenv`，但该文件只 include 了 `<algorithm> <utility> <vector>`（同样由 `c442044` 引入），**没有 `<cstdlib>`**——当前靠传递包含侥幸通过。

**证据链（为什么提交时声称「14/14 全绿」）：**

| 时间 | 事实 |
| --- | --- |
| 2026-10-13 12:03 | `build/native/host/libZhulongEngine.a` 与全部 test 二进制最后一次成功产出 |
| 2026-10-13 12:03 | `build/native/host/CMakeFiles/ZhulongEngine.dir/src/abi/engine.cpp.o` 时间戳 |
| **2026-10-14 08:51** | **`c442044` 提交，commit message 声称 `make native-test` 14/14** |
| 2026-10-14 08:56 | `native/src/abi/engine.cpp` 最后修改（提交之后） |

```txt
$ nm build/native/host/libZhulongEngine.a | grep -c Zhulong_stream_get_broker_path
0
```

归档中**不含**本轮新增的 C ABI 符号 → 当时的「14/14」跑的是 10-13 的陈旧二进制，`engine_abi_test.c` / `engine_test.cpp` 中针对新 ABI 的断言并未真正执行。**结论：这不是设计缺陷，是提交前门禁未真正执行。**

**影响面**：`make native-test`、`make native-build`、`make go-check`（依赖 `native-build`）全部无法执行。任何依赖 `internal/engine` 的验证都失去基线。

### 1.2 缺陷 B：`frontend-check` 目标损坏

```make
frontend-check: frontend-install
	$(NPM) run lint --prefix web
	$(NPM) run type-check --prefix web   # ❌ script 不存在
	$(NPM) test --prefix web
	$(NPM) audit --prefix web            # ❌ script 不存在
	$(NPM) run build --prefix web

frontend-install:
	$(NPM) ci --prefix web               # ❌ 仓库只有 pnpm-lock.yaml
```

实测：

| 项 | 实际 |
| --- | --- |
| `web/` 锁文件 | 只有 `pnpm-lock.yaml`（`lockfileVersion: '9.0'`），**无** `package-lock.json` |
| `npm ci --prefix web` | 失败（npm 要求 `package-lock.json` / `npm-shrinkwrap.json`） |
| `web/package.json` 的 scripts | `lint` ✅ / `test` ✅ / `build` ✅ / `type-check` ❌ **不存在** / `audit` ❌ **不存在** |
| 可用的等价物 | 类型检查已含在 `build`（`tsc -b && vite build`）；格式化检查为 `format:check`（prettier） |
| 仓库声明的包管理器 | `pnpm`（`pnpm-lock.yaml` + `pnpm-workspace.yaml`），本机 pnpm 12.9.1 可用 |

## 2. 目标

让 `make check` 在干净树上可执行、可复现，且**不引入任何产品行为变更**。

## 3. 需求

| ID | 需求 |
| --- | --- |
| R1 | `native/src/abi/engine.cpp` 补 `#include <cstring>`，`native/src/pipeline/engine.cpp` 补 `#include <cstdlib>`。⚠️ **只补 include**，不改任何逻辑、不重排既有 include。 |
| R2 | 干净重建验证：native 归档与全部测试目标真正重新编译，`nm` 能查到 `Zhulong_stream_get_broker_path` / `Zhulong_stream_set_publish_rate` 符号。 |
| R3 | `frontend-install` 改用仓库声明的包管理器（pnpm），使 `make frontend-check` / `make frontend-build` / `make check` 可执行。⚠️ 需要 pnpm 缺席时的行为明确（显式失败或回退），不得静默跳过。 |
| R4 | `frontend-check` 中不存在的 `type-check` / `audit` 两个 script 按**等价或删除**处理，并说明取舍；不得为通过门禁而新增无实质内容的空 script。 |
| R5 | 修完后 `make native-test` 与 `make go-check` 必须**真绿**（非复用陈旧产物）。 |

## 4. 范围

**包含**：两个 native include 修复、Makefile 的 frontend 目标修复、干净重建验证。

**不包含**：`10-13-algo-broker-host` 的 AC2/AC6/AC7/AC8/AC10/AC11 补齐（留在原任务）、`internal/camera` 的 `TestStreamHubOnDemandAcquireAndRelease` 既有竞态（见 §6）、任何功能或架构改动。

**不做 `design.md` 的理由**：本任务无架构或契约决策——缺陷 A 是两处缺失的 include，缺陷 B 是把 `frontend-check` 对齐到仓库实际声明的包管理器与既有 scripts。唯一需要取舍的点（`type-check`/`audit` 如何处置、`NPM` 变量语义）已在 `implement.md` 阶段 C 内联说明，不需要独立设计文档。

## 5. 验收标准

| ID | 关联 | 可验证结果 |
| --- | --- | --- |
| AC1 | R1、R2 | 删除 `build/native/host` 后 `make native-test` 全绿；`nm build/native/host/libZhulongEngine.a \| grep -c Zhulong_stream_get_broker_path` **非 0** |
| AC2 | R1 | `g++ -fsyntax-only` 逐文件预检 `native/src/**/*.cpp` 无 error |
| AC3 | R3 | `make frontend-check` 可执行并通过（或明确记录剩余阻塞及其原因） |
| AC4 | R4 | 不存在的 script 已按等价/删除处理，`make frontend-check` 中不再引用不存在的 target |
| AC5 | R5 | `make native-test` + `make go-check` 真绿，且**附上归档符号证据**证明非陈旧复用 |

## 6. 明确不承诺

- 不承诺修复 `internal/camera` 的 `TestStreamHubOnDemandAcquireAndRelease` 既有竞态（journal 已用 `git stash` 在改动前的树上复现，属既有问题，建议单开任务）。
- 不承诺前端依赖可离线安装（`pnpm install` 需要网络或已填充的 store）。
- 不承诺补齐 `broker-host` 的任何 AC。

## 7. 残余风险

- ⚠️ **根因是流程而非代码**：`c442044` 声称的验证未真正执行。补完本任务后需确认「改完 C++ 必须 `make native-build`」与「提交前 `make check`」的约束在后续任务中真正被遵守（项目 `AGENTS.md` 已有该约束，但本次未被遵守）。
- ⚠️ `Makefile` 的 `NPM ?= npm` 变量名与 pnpm 切换的兼容性：若改为 pnpm，变量语义需一并明确（改名或保持 NPM 但值为 pnpm），避免留下第二个真实源。
