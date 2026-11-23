# Verify Report: 2026-11-23-camera-alert-form-drawer-sheet

- Verdict: **PASS**（含 1 项契约文字偏差与 5 项非阻塞 Note，见「偏差与发现」）
- base_commit: `b62407bc308c8a63a0e9de51fc693505d9aebc75`
- Worktree: `/home/niko/dev/Go/Zhulong/.luoshu/worktree/2026-11-23-camera-alert-form-drawer-sheet`
- Mode: full / stage=verify（4/4 任务已完成）
- `luoshu changes validate 2026-11-23-camera-alert-form-drawer-sheet` → `change '2026-11-23-camera-alert-form-drawer-sheet' valid (4/4 tasks complete)`，exit 0
- 变更集（`git diff --stat <base>`）：5 files changed, 398 insertions(+), 152 deletions(-)；忽略空白后 295 insertions(+), 49 deletions(-)（与「容器/缩进重构」描述一致）
- 变更文件：`web/src/features/cameras/components/cameras-action-dialog.tsx`、`web/src/features/cameras/components/cameras-action-dialog.test.tsx`、`web/src/features/alert-push/components/alert-destination-dialog.tsx`、`web/src/features/alert-push/components/alert-destination-dialog.test.tsx`、`web/src/features/alert-push/components/form/destination-form.tsx`
- `git status --porcelain`：仅上述 5 个 `M` + 未跟踪 `.luoshu/changes/2026-11-23-camera-alert-form-drawer-sheet/`（预期）

## Gate Results

`operations.verify.gates` 为空（`.luoshu/config.yaml`），下表为 verify.md 执行命令与我另行补充的定向检查，全部由 Verify 现场重跑，未采信 Apply 阶段数字。

| Gate | Command（均在 `web/`） | 结果 | 状态 |
| --- | --- | --- | --- |
| 类型检查（`pnpm type-check` 不存在，用权威等价命令） | `npx tsc -b --force` | exit 0，无诊断输出 | PASS |
| 摄像头组件测试 | `pnpm test -- src/features/cameras/components/cameras-action-dialog.test.tsx` | 1 file / **4 passed**，0 failed（`renders create dialog…`、`updates a GB28181 camera…`、`retains user draft on 409 CAS conflict…`、`ignores close requests while a submit is pending`） | PASS |
| 告警推送组件测试 | `pnpm test -- src/features/alert-push/components/alert-destination-dialog.test.tsx` | 1 file / **9 passed**，0 failed（含新增 `keeps the drawer footer pinned when custom headers grow`） | PASS |
| 特性全量回归 | `pnpm test -- src/features/cameras/ src/features/alert-push/` | **11 files / 63 tests passed**，0 failed，exit 0 | PASS |
| 定向 lint（改动文件） | `npx eslint <5 changed files>` | exit 0，无输出 | PASS |
| 定向格式（改动文件） | `npx prettier --check <5 changed files>` | `All matched files use Prettier code style!` | PASS |
| 结构校验 | `luoshu changes validate <id>` | valid (4/4 tasks complete) | PASS |
| 附加（非门禁） | `pnpm knip` | 报 2 个新增未使用导出（`CamerasActionDrawer`、`AlertDestinationDrawer`），基线已有 80 个 unused exports；knip 不属于 `make check`（`.luoshu/specs/frontend/quality-guidelines.md` §1） | Note |

环境事实（已在报告中登记，未做任何「修复」）：
- `web/node_modules` 指向主检出目录，`pnpm test` 每条命令都会打印 `ERR_PNPM_STORE_DIR_OPEN_OPERATION_LOCK … Read-only file system (os error 30)` 与 “The install that runs before scripts failed”，但 `node ./run-browser-tests.mjs` 正常执行，测试确实跑在真实 Chromium（Vitest 4.1.4 + Playwright）中。
- 这两个测试文件只命中 `chromium-default`（414x896）；`chromium-desktop`（1280x896）的 `include` 仅含 3 个无关文件（`web/vite.config.ts:78-101`）。

## Contract & Checklist Evaluation

### V1 摄像头抽屉化与交互连续性 — **PASS**

- 右侧抽屉与容器替换：`cameras-action-dialog.tsx:368-374` 为 `SheetContent side='right' className='flex h-full w-full flex-col gap-0 p-0 sm:max-w-xl md:max-w-2xl'`；测试断言 `[data-slot="dialog-content"]` 已不存在（`cameras-action-dialog.test.tsx:117`），并断言 `[data-slot="sheet-content"]` 右缘与视口右缘误差 ≤1px、`top === 0`，且在临时把 `maxWidth` 收窄到 240px 后右缘仍贴住视口（`:118-146`），证明是「右锚定」而非「左锚定 + 撑满」。
- 头部标题/说明/关闭：`SheetHeader`（`:186-204`）内含 `SheetTitle`/`SheetDescription` + `Video` 图标，`pe-14` 为内置关闭按钮（`sheet.tsx:76-81`，`absolute inset-e-4 top-4`）留位；测试断言 `role=button[name="Close"]` 存在（由 Sheet 原语提供，非业务实现）。
- 中间表单独立滚动：`:206-209` `data-testid='cameras-action-scroll'` + `min-h-0 flex-1 overflow-y-auto px-6 py-4`；测试断言 `getComputedStyle(scroll).overflowY === 'auto'`、`scroll.scrollWidth <= scroll.clientWidth + 1`（无横向溢出）、`scroll.bottom <= footer.top + 1`，并在 `scrollTop = 10000` 后断言 footer/抽屉外框位置不变。
- 底部吸附操作栏：`:310-347` `SheetFooter … mt-0 shrink-0 border-t … px-6 py-3.5 sm:flex-row sm:items-center sm:justify-between`；取消为 `SheetClose asChild`（`:324`，`disabled={isPending}`），提交按钮在表单外通过 `form='camera-action-form'`（`:331`）关联。测试断言 `footer.bottom === sheet.bottom`，点「保存更改」实际触发 `updateCamera`（既有 GB28181 用例）。
- 待提交期关闭防护：`onOpenChange` 守卫 `:360-367`（`if (!isPending && !val) setOpen(null)`，该守卫在 base 版本已存在，本次沿用并补注释）；新增用例 `ignores close requests while a submit is pending`（`cameras-action-dialog.test.tsx:272-305`）在 `updateCamera` 挂起时发送 `{Escape}`，断言抽屉标题仍在，resolve 后抽屉消失。按钮侧：提交按钮 `disabled={isPending}` 且渲染 `Loader2` 转圈（`:329-343`），取消按钮 `disabled={isPending}`，底栏左侧显示探测中提示（`:311-321`）。
- 业务逻辑零改动：忽略空白 diff（`git diff -w`）显示仅容器/布局变化；Zod/`createCameraFormSchema`、409 CAS 保留草稿 + 刷新 revision、GB28181 禁用 RTSP 编辑等逻辑行均未删改，对应既有断言全部保留且通过。

### V2 告警推送抽屉化与长表单流式体验 — **PASS**

- 右侧抽屉：`alert-destination-dialog.tsx:31-34` 同样的 `SheetContent side='right'` 外壳；头部（`:35-53`）按 `channelType` 渲染 `Radio`/`Globe` 图标 + `SheetTitle`/`SheetDescription`，`pe-14` 避让关闭按钮；测试断言无 `dialog-content`、右缘贴视口、`top === 0`（`alert-destination-dialog.test.tsx:145-162`）。
- 长表单滚动流：`form/destination-form.tsx:359-361` `<form className='flex min-h-0 flex-1 flex-col'>`，滚动区 `:363-433`（`data-testid='alert-destination-scroll'`，`min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-4`）承载错误横幅 + 通道卡片 + Webhook/MQTT 分区 + 事件过滤 + 存活上报；Footer `:434-524` 使用 `SheetFooter`，`mt-0 shrink-0`。测试断言 footer 贴抽屉底边、header/footer 在滚动后位置不变、滚动区无横向溢出（`:163-181`）。
- 通道类型切换与 Headers 增删：既有用例 `seamlessly switches protocol fields and MQTT LWT options…`、`sends undefined for untouched headers and empty object {}…` 全部保留并通过；新增 `keeps the drawer footer pinned when custom headers grow`（`:357-386`）连续点 3 次「添加标头」，断言「移除标头」按钮变为 4 个、`scroll.scrollWidth <= clientWidth + 1`、footer 仍贴抽屉底边。
- 提交成功后关闭并刷新外部列表：`alert-destination-dialog.tsx:56-64` 在 `DestinationForm` 的 `onSuccess` 中先 `onSuccess()` 再 `onOpenChange(false)`；测试 `:352-354` 断言 `onSuccess` 被调用 2 次（该用例确为两次成功提交：先不改 Headers 提交、再清空 Headers 提交）且 `onOpenChange` 最后一次实参为 `false`。
- 业务逻辑零改动：`destination-form.tsx` 忽略空白 diff 仅 14 行（form className、滚动容器、`DialogFooter`→`SheetFooter`），无字段/校验/请求形态变化。

### V3 前端类型与真实浏览器测试门禁 — **PASS**（含契约命令偏差 D1）

- 类型检查：`npx tsc -b --force` exit 0、无诊断。`pnpm type-check` 在 `web/package.json` 的 `scripts` 中**不存在**（脚本为 dev/build/lint/preview/format/format:check/knip/test/test:watch/test:ui/test:browser/test:coverage/test:browser:install），`Makefile:56-58` 与 `.luoshu/specs/frontend/quality-guidelines.md` §1.1 明确「`type-check` 不是独立 script，类型检查由 build 的 `tsc -b` 承担」。故按契约意图以 `npx tsc -b --force` 作为权威等价校验，判定 V3 意图达成（契约文字缺陷见 D1）。
- 真实浏览器测试：两文件 4/4、9/9；特性目录 11 files / 63 tests 全通过，均在真实 Chromium 内执行（非 JSDOM）。未发现 `it.skip` / `it.todo` / `it.only` / `describe.skip` / `test.skip`。
- 断言未弱化：两测试文件的删除行合计仅 1 行 —— `onOpenChange={vi.fn()}` 改为具名 spy（`alert-destination-dialog.test.tsx:314,317`），属增强；其余全部为新增（+105 / +114 行）。未发现对既有行为断言（Headers 语义、MQTT LWT、探测、409 CAS、错误横幅、禁用态）的删除或放宽。
- 规范遵守：见下节。

### 负向边界审计 — 全部 PASS

1. **接口契约不变量（Go/API/信封）**：`git diff --name-only <base>` 仅 5 个 `web/src/features/**` 文件，无 `internal/`、无 `internal/httputil`、无 API 客户端/解包/Zod 解析改动 → 未触碰。PASS
2. **表单校验规则不变量**：`web/src/features/cameras/data/schema.ts`、`web/src/features/alert-push/data/schema.ts` 及 `types` 均不在 diff 中；两个组件仍以原 schema 驱动 `zodResolver`，字段未增删改名 → 未放宽。PASS
3. **作用域防护**：`channel-task-dialog.tsx`、`persons-*`（含 persons 表单/面容抽屉）、`*-api-token*`、`gb28181-*`、`*-delete-dialog.tsx`、`cameras-dialogs.tsx`、`alert-push-page.tsx`、`components/ui/{dialog,sheet}.tsx`、`vite.config.ts`、`routeTree.gen.ts` 均未出现在 diff 中 → 未越界。PASS
4. **测试无妥协**：测试只 mock 传输层（`../hooks/use-cameras`、`../api/alert-push-api`、`@/features/algorithms/api/algorithms-api`、`@/features/cameras/api/cameras-api`），被测组件为真实渲染；新增断言为真实 DOM 几何/角色探针，无 `sleep` 固定等待、无 `as any`/`@ts-ignore`/`eslint-disable` 新增。PASS

### 任务的增量验证命令（tasks.md）

- 1.1 / 1.2 / 1.4 的 `cd web && pnpm test -- …` 均实际可执行并通过（上表）。
- 1.3 的 `cd web && pnpm type-check` 不存在 → 见 D1（用 `npx tsc -b --force` 等价覆盖，类型契约达成）。

## Rules & Specs Compliance

- **触发规则（mounted）**
  - `web/frontend-api-boundary`：`trigger: glob` 仅匹配 `web/src/lib/api-client.ts`、`web/src/features/**/api/**`、`web/src/features/**/data/**`；本次 diff 不含这些路径 → **未触发**；同时确认无信封拆包、无 `as` 断言替代 Zod 的新代码。
  - `web/frontend-browser-tests`：**触发并遵守**。测试与被测组件同目录共置；真实 Chromium（`web/vite.config.ts:56-101`）；异步/动画落位用 `vi.waitFor`（`settledSheet`，`alert-destination-dialog.test.tsx:90-101`）而非固定 sleep；无 JSDOM 假设。§2.5「断言必须能真的失败」：新增几何断言对「footer 未 shrink-0 / 未 flex 布局 / 左侧锚定」确实会失败（本次以真实布局通过）；滚动位移断言强度不足处记为 Note N1。
  - `web/frontend-route-boundary`：**未触发**（无 `src/routes/**`、`routeTree.gen.ts`、`features/**/index.tsx`、`vite.config.ts` 改动）。
- **挂载规格（mounted specs）**
  - `frontend/index.md`：100% 复用 `@/components/ui/*`（`Sheet` 系列，无自造基础标签）；可见文案全部走 `t(...)`（新增行仅注释/`data-testid`/className，无硬编码中英文）；OKLCH 语义色与逻辑方向类（`inset-e-*`、`pe-14`）保持一致；未引入明文 `any`。
  - `frontend/development-guidelines.md`：§13 shadcn-admin 视觉与组件规范满足；§8/§165「模态留存级」语义保留（409/探针失败仍在内容顶部渲染 Banner 并保留草稿，测试 `retains user draft on 409 CAS conflict…` 通过）。§8 的「弹窗开关统一 `@/hooks/use-dialog-state`」现状为既有实现（cameras 用 `CamerasContext`、alert 用 props 透传），本次未新增偏离。
  - `frontend/quality-guidelines.md`：门禁映射一致（`type-check` 非独立 script、类型检查在 `build` 的 `tsc -b`）；测试共置与真实浏览器要求满足；定向 `prettier`/`eslint` 均通过。

## Deviations（需在 Archive 阶段处理的文档性偏差，非实现缺陷）

- **D1（Warning）契约命令不存在**：`verify.md` V3「验证执行命令 #1」与 `tasks.md` 1.3 均写 `cd web && pnpm type-check`，但 `web/package.json` 无该 script（执行会报 `Missing script: "type-check"`）。权威等价为 `npx tsc -b`（`make frontend-build`/`pnpm build` 亦以 `tsc -b && vite build` 承担类型检查，见 `Makefile:56-58`）。本次以 `npx tsc -b --force` 执行（exit 0）。建议归档时把 `verify.md` 与 `tasks.md` 的命令改为 `cd web && npx tsc -b`（或 `pnpm build`），避免后续变更继续引用不存在的门禁。
- **D2（Note）新增未被消费的别名导出**：`cameras-action-dialog.tsx:388` `export const CamerasActionDrawer = CamerasActionDialog`、`alert-destination-dialog.tsx:71` `export const AlertDestinationDrawer = AlertDestinationDialog`。该别名在 `design.md` §3 有声明，但 `proposal.md`/`verify.md` 未提；`pnpm knip` 把它们计入 unused exports（仓库基线已有 80 项 unused exports，knip 不在 `make check` 内）。归档时可决定：在调用点改用 Drawer 别名，或移除别名仅保留原名。
- **D3（Warning）影响范围清单缺口**：`proposal.md` 的「涉及文件」未列 `web/src/features/alert-push/components/form/destination-form.tsx`，但 `design.md` §2.B 明确要求把底部操作栏下沉到 `DestinationForm` 内；实际 diff 含该文件（忽略空白仅 14 行，纯布局）。建议归档时补齐「影响范围」清单，避免下次评审误判越界。
- **D4（Note）新增测试钩子**：`data-testid='cameras-action-scroll'` / `'alert-destination-scroll'`（kebab-case，与仓库既有 `data-testid` 命名一致），仅用于几何断言，无生产行为影响。
- **D5（Note）环境性告警**：worktree 的 `web/node_modules` 为指向主检出的符号链接，`pnpm` 每条命令打印 store lock / 只读文件系统告警；不影响测试执行，未做改动。

## Findings

- [Note] N1 `web/src/features/cameras/components/cameras-action-dialog.test.tsx:150-160`（及 `alert-destination-dialog.test.tsx:176-181`）：`scroll.scrollTop = 10_000` 之后断言 header/footer 不动，但没有先断言 `scroll.scrollHeight > scroll.clientHeight` 或 `scroll.scrollTop > 0`；若内容恰好未溢出，这段「滚动时不动」是平凡通过。未削弱既有断言、不影响契约判定，建议后续补一条溢出前置断言，使该用例在实现回退（例如误删 `min-h-0`）时必然失败。
- [Note] N2 `web/src/features/cameras/components/cameras-action-dialog.tsx:360-367`：待提交期 `isPending` 守卫会拦截内置 X 关闭按钮（`web/src/components/ui/sheet.tsx` 的 `SheetPrimitive.Close` 未接 `disabled`），点击表现为「无响应」而非视觉禁用；行为安全（不会丢失中间态），如需更明确反馈可在后续变更中让 X 也进入 disabled 态。
- [Note] N3 `web/src/features/cameras/components/cameras-action-dialog.tsx:206-213`：409 冲突/探测失败 Banner 现在位于滚动区内（旧实现固定在滚动区之上）。仍在内容顶部且「模态留存 + 草稿保留」语义未变，但用户滚到底部时可能看不到新出现的冲突提示；如需「始终可见」可把 Banner 移出滚动区。
- [Note] N4 桌面宽度仅静态证据：这两个测试文件只跑 `chromium-default`（414x896），没有 1280 宽视口断言。独立核验的 CSS 证据：`cn()`（clsx + tailwind-merge）实际合并结果为 `… w-full … sm:max-w-xl md:max-w-2xl`，`sm:max-w-sm` 与 `w-3/4` 被移除；Tailwind 主题 token `--container-xl: 36rem`、`--container-2xl: 42rem`、`--breakpoint-sm: 40rem`、`--breakpoint-md: 48rem`（`web/node_modules/tailwindcss/theme.css:327-340`），即 ≥640px 时抽屉 576px、≥768px 时 672px，1280px 视口下左侧仍有约 608px 列表可见（遮罩 `bg-black/50` 为 Sheet 原生行为，列表可见但变暗）。如需自动化证据，可在归档后为该两文件补 `chromium-desktop` 用例。
- [Note] N5 `web/src/features/alert-push/components/alert-destination-dialog.tsx:26`：`Sheet open onOpenChange` 直接透传，提交在途时 ESC/遮罩可关闭抽屉——与改造前的 `Dialog` 行为一致，`verify.md`/`design.md` 未要求该抽屉加 pending 守卫，故不作为缺陷；若产品口径要求与摄像头抽屉对齐，需另开变更。

## 防篡改记录（源文件只读证明）

写报告前采集的基线指纹（写报告后复算应完全一致，源文件未被修改）：

- `sha256(git diff <base>)` = `9ea754e46f483eab250ecede2ed677e71f7b83fbeb3e73c8486dff03605a3788`
- `sha256(git status --porcelain)` = `842d1dd7d01c5aa3f9f40eb064503b83af3b3c28e62d0e77d98eed17c29f190e`
- `sha256(5 个变更文件的内容哈希)` = `9bf14c22d4191bd40ce14991152fc459b9d18e163b6a0e7795280ab21d08a0ad`
- 全部门禁执行后 `git status --porcelain` 仍只有 5 个 `M` 与未跟踪的变更目录；`npx tsc -b --force` 未产生被 Git 跟踪的新文件。

本 Verify 会话仅写入本文件 `verify-report.md`；未修改任何业务源码、测试、`proposal.md`、`design.md`、`tasks.md`、`verify.md`、`context.yaml`、`.luoshu/config.yaml`。

## 结论

- 实现与 `verify.md` V1/V2/V3 的意图一致，负向边界 4 项全部满足，3 条 mounted rules 与 3 份 mounted specs 无违规，所有门禁命令现场重跑通过。
- **Verdict: PASS**；归档前建议仅处理文档性偏差 D1（`pnpm type-check` → `npx tsc -b`）与 D3（补齐 `destination-form.tsx` 到影响范围），并决定 D2（Drawer 别名保留或消费）。N1–N5 为非阻塞改进项。

## 验证器输出

- `luoshu verify validate-report 2026-11-23-camera-alert-form-drawer-sheet` → `verify-report.md valid=true verdict=PASS`，exit 0。
- `luoshu verify assert-unchanged … --baseline <报告写入前快照>` → exit 1，唯一差异项为 `.luoshu/changes/2026-11-23-camera-alert-form-drawer-sheet/verify-report.md`（即本次 Verify 唯一被允许写入的文件）；以报告写入后状态重新取基线再跑 `assert-unchanged` → exit 0，无其他改动，证明业务源码与测试未被触碰。
- 复算指纹与上文一致：`DIFF_SHA256=9ea754e46f483eab250ecede2ed677e71f7b83fbeb3e73c8486dff03605a3788`、`STATUS_SHA256=842d1dd7d01c5aa3f9f40eb064503b83af3b3c28e62d0e77d98eed17c29f190e`、`FILES_SHA256=9bf14c22d4191bd40ce14991152fc459b9d18e163b6a0e7795280ab21d08a0ad`。
