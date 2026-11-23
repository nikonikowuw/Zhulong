# Verify Report: 2026-11-23-unify-list-page-shell

- Verdict: PASS
- base_commit: `0bc6d7e142b50ba3f1a7a677554fb1b1ab6f2211`
- Worktree: `/home/niko/dev/Go/Zhulong/.luoshu/worktree/2026-11-23-unify-list-page-shell`
- Branch: `luoshu/2026-11-23-unify-list-page-shell`
- Reviewed at: 2026-11-23
- Diff scope: 21 tracked files (`+754 / -942`) + 5 new source/test files + 5 change docs; `luoshu verify run --json` reports `changedFiles: 31`, `ready: true`, `gatesPassed: true`, `validationErrors: []`, `incompleteTaskIds: []`.

## Gate Results

`operations.verify.gates` is empty in `.luoshu/config.yaml` (`gates: []`), so there is no configured gate command to execute. `luoshu verify run --json` reports `gates: []` and `gatesPassed: true`.

| Gate | Status | Evidence |
| :--- | :--- | :--- |
| `operations.verify.gates` | N/A | `.luoshu/config.yaml` `operations.verify.gates: []`; run JSON `gates: []`; `gatesPassed: true` |
| `tasks.md` completeness | PASS | 11/11 checked (`[x]`); run JSON `incompleteTaskIds: []` |
| Worktree scope | PASS (Note) | 21 modified + 5 new source/test files; only two files not enumerated in `proposal.md` impact table (`web/src/components/data-table/index.ts`, `web/src/features/persons/index.test.tsx`), both mechanically required by the migration / explicitly referenced by V5 |

## Contract & Checklist Evaluation

- [PASS] V1 — `web/src/components/list-page.tsx:34-40` renders `<Header fixed>` with `Search` / `LanguageSwitch` / `ThemeSwitch` / `ConfigDrawer` / `ProfileDropdown`; `:42` `Main className='flex flex-1 flex-col gap-4 sm:gap-6'`; `:43-46` `[data-slot='list-page-header']` + `flex flex-wrap items-end justify-between gap-2`; `:47` heading `min-w-0 flex-1`; `:49-53` description `max-w-3xl`; `:55-62` actions slot only when `actions` is passed. `web/src/components/list-page.test.tsx:25-53` asserts `Header fixed`, header/heading/description/actions, and `:56-70` asserts the actions node is absent when omitted; test file 2/2 green. Note: the test mocks `@/components/layout/header` and therefore does not assert the real `Main` class or the `gap-2` token; both were inspected in source.
- [PASS] V2 — `web/src/components/data-table/shell.tsx:72-75` container is `cn('rounded-md border border-border bg-card', className)`; no page passes `className` to `DataTableShell`, so the merged DOM class is exactly `rounded-md border border-border bg-card`. `:127` renders pagination only when `totalRows > 0`. `shell.test.tsx:81-96` asserts empty text + zero buttons for `totalRows = 0`; `:98-103` asserts page buttons for `totalRows > 0`; test file 6/6 green. Note: the test uses `toContain('rounded-md')` etc. rather than exact string equality, so it would not catch an extra class; source inspection covers the exact-class requirement.
- [PASS] V3 — `shell.tsx:101-122` body priority is `isLoading → isError → rows.length > 0 → emptyState`; `:103` repeats `loadingRow` `Array.from({ length: Math.max(skeletonRows, 1) })` (default `skeletonRows = 5`); `:115-120` wraps `renderRow(row)` in `<Fragment key={row.id}>`; `:169-172` applies `cn(meta?.className, meta?.tdClassName)` to `td`. `shell.test.tsx:105-121` (loading 2 rows / 2 cells, empty text absent), `:123-142` (error text present, empty text absent), `:144-164` (renderRow returns two `TableRow`s) all pass. Notes: (a) the contract's "two-row Fragment → 4 `tr`" case is exercised with one data row (`data={[people[0]]}`) producing 2 `tr`, not with two data rows producing 4; source inspection confirms the per-row Fragment would yield 4. (b) the test asserts `meta.tdClassName` on `td` but not `meta.className`; source applies both.
- [PASS] V4 — `web/src/components/data-table/column-header.tsx:31-46` unsortable branch is `cn('relative start-3 -mx-3 flex h-8 items-center px-3', className)`; `:48-87` sortable branch still renders `<Button variant='ghost' size='sm' className='h-8 ...'>` with the asc/desc/hide menu. `column-header.test.tsx:54-71` asserts both wrappers round to 32px and the plain wrapper contains `h-8`, `items-center`, `px-3`; test file 2/2 green. Notes: the test does not assert the ghost `Button`/`variant`/`h-8` (source inspected), does not assert `-mx-3`/`start-3`, and does not exercise 414px overflow/row-hit behavior. See Finding W3 for the design-doc drift.
- [PASS] V5 — all five table tests hit `[data-slot='data-table-container']` and `[data-slot='data-table-toolbar']`: `persons-table.test.tsx:65/68`, `cameras-table.test.tsx:94/97`, `algorithms-table.test.tsx:141/144`, `AuditTable.test.tsx:76/79`, `tasks-table.test.tsx:142/145`; all green in the 9-file run. Page title: `persons/index.test.tsx:121` asserts the `人员管理` heading; the other four `ListPage title` values were inspected in source/locale (`摄像机管理`, `算法包管理`, `审计日志`, `视频分析任务`). Note: only persons has an automated page-title assertion; the four table tests render the table in isolation, so their page titles are source-verified rather than test-asserted. See Finding W4.
- [PASS] V6 — `web/eslint.config.js:59-88` adds `no-restricted-imports` for `@/components/layout/header` and `@/components/layout/main` with `files` exactly the five `src/features/{persons,cameras,algorithms,audit,tasks}/index.tsx` entries. `grep` over the five entries finds no such imports; `pnpm exec eslint` on the five entries exits 0 with 0 errors.
- [PASS] V7 — the nine targeted files pass 53/53; the full browser suite passes 200 files / 1389 tests + 1 skipped across six shards at `--maxWorkers=1`. The five table tests' assertion counts are net +2 versus base (`persons 4→6`, `cameras 0→2`, `algorithms 10→12`, `audit 2→4`, `tasks 10→12`), no existing assertion was deleted. Covered interactions include persons grid switch/bulk select, cameras selection-under-sort/health multi-filter, tasks expand/collapse/filter, and algorithms row-detail/delete. Note: algorithms sorting and cameras/audit pagination are not directly asserted in the touched table tests; the pre-existing `persons-table-pagination.test.tsx` covers persons pagination and all existing suites remain green. This is a coverage limitation, not a detected regression.
- [PASS] V8 (environment caveat) — `make frontend-check` cannot reach its checks in this sandbox: `frontend-install` runs `pnpm --dir web install --frozen-lockfile` and fails with `ERR_PNPM_STORE_DIR_OPEN_OPERATION_LOCK ... Read-only file system (os error 30)`, make exit 2. The constituent checks were run individually and are green: `prettier --check` on all changed files, `eslint .` (0 errors / 4 warnings), `tsc -b`, six-shard browser suite (200 files / 1389 passed + 1 skipped), and `vite build`. Per instruction, this is recorded as an environment limitation, not a code defect.

- [PASS] N1 — `git status --porcelain` contains no `web/src/components/ui/**` path; the UI primitives (`table.tsx` etc.) were not modified.
- [PASS] N2 — no `web/src/features/*/api/**` changes and no `manualPagination` / `manualSorting` / `manualFiltering` value changes. persons still has `manualPagination: true` + `manualSorting: true` (`persons-table.tsx:130`), audit still has manual pagination/filtering; cameras/algorithms/tasks keep their prior model (tasks' `manualFiltering: true` is pre-existing context). No API files appear in the diff.
- [PASS] N3 — no `gb28181`, `records`, `image-search`, `live`, or `spatial` page/table files in the diff; `web/src/features/gb28181/components/gb28181-devices-table.tsx` is untouched, including its `rounded-xl` container.
- [PASS] N4 — no changes under `web/src/locales/**`; no new business keys in `common.json`.
- [PASS] N5 — the nine touched test files contain no `.skip`, `.only`, or `expect.soft`; diff inspection shows only additions (the two removed lines are destructuring lines updated to add `container`), and the five table tests' `expect(` counts are net +2 each.
- [PASS] N6 — no Go files, `internal/webui/dist`, `web/src/routes/**`, or `web/src/routeTree.gen.ts` in the diff.

Static checklist:

- [PASS] C1 — `git diff --stat` covers the proposal's shared components, five page entries/tables, spec, eslint config, and tests. Note: `web/src/components/data-table/index.ts` (barrel export of `DataTableShell` / `DataTableToolbarRow`) and `web/src/features/persons/index.test.tsx` (V5's page-title assertion) are not enumerated in the proposal impact table; both are mechanically required and no unrelated module appears.
- [PASS] C2 — `theme-and-i18n.md` §4.1 (lines 96-134) no longer uses "照抄 persons / cameras"; its clauses name `ListPage`, `DataTableShell`, `DataTableColumnHeader`, their slots, and `data-slot` structure.
- [WARN] C3 — the spec's judgment coverage is partial: the "分页模型决定排序能力" table covers server pagination → no client sort and the client-pagination precondition, but the spec does not state "视图切换仅在有 grid 分支时" (design.md:156 and tasks.md:24 require it), and the checklist's "自定义工具栏走 `toolbarActions`" conflicts with `theme-and-i18n.md:112` / `design.md:168` (custom toolbar goes to `toolbar`). The conflict is recorded as a contract ambiguity (see Finding W2); the missing grid condition is a documentation gap (W1). Neither changes a V/N behavior: persons is the only page with a view switch and it has a grid branch.
- [PASS] C4 — the exception whitelist at `theme-and-i18n.md:128-130` lists `gb28181` (settings, `rounded-xl`), `records` (`RecordsListShell`), and `image-search` / `live` / `spatial` (workbenches), each with a reason.
- [PASS] C5 — `web/eslint.config.js:59-88` restricts only the five entry files; no other page is covered by the `no-restricted-imports` block.
- [PASS] C6 — `DataTableShell` has no `useTranslation` import and no new i18n dependency; the diff shows no locale/package changes.

## Rules & Specs Compliance

Matched rules:

- `web/frontend-browser-tests` (`.luoshu/rules/web/frontend-browser-tests.md`): the new tests are co-located `*.test.tsx`, run in the real Chromium (vitest browser), use `await render`/`expect.element`-style waiting rather than JSDOM assumptions; the six-shard suite passes. Compliant.
- `web/frontend-route-boundary` (`.luoshu/rules/web/frontend-route-boundary.md`): the change touches no `src/routes/**` or `routeTree.gen.ts`; the five entries import the shared `ListPage` and their own feature components, keeping the feature public-entry boundary. Compliant.

Mounted specs:

- `frontend/theme-and-i18n.md` — §4.1 is the changed contract; the implementation matches its component/slot structure. The spec itself is missing the grid-branch condition (W1); this is a documentation gap, not an implementation violation.
- `frontend/development-guidelines.md` §2.2 — the shell puts `group` on the default `TableRow` (`shell.tsx:162-164`), and the custom renderRows in persons/cameras/algorithms/audit/tasks also carry `group`; the sticky action cells keep `bg-card` and the two-layer `before` pseudo-elements. Compliant.
- `frontend/directory-structure.md` — new shared components live in `web/src/components/` and `web/src/components/data-table/`; tests are co-located. Compliant.
- `frontend/quality-guidelines.md` — `make frontend-check` is format:check + lint + test + build; all constituents are green individually; the dependency-install step is blocked by the sandbox (W5). Compliant with the environment caveat.
- `frontend/index.md` — list pages use the shared list-page/table shell; no route-level changes. Compliant.

## Findings

- [Warning] `.luoshu/specs/frontend/theme-and-i18n.md:113` — the `toolbarActions` row says "视图切换等附加控件" but does not state the design's hard condition "仅在有 grid 分支时" (`design.md:156`, `tasks.md:24`). Add the condition so a future page without a grid branch cannot add a view switch. This is the only checklist gap (C3).
- [Warning] `verify.md:50` vs `theme-and-i18n.md:112` / `design.md:168` — checklist says "自定义工具栏走 `toolbarActions`"; spec and design route custom toolbars (e.g. `AuditToolbar`) to `toolbar`, and reserve `toolbarActions` for view-switch-style extras. The two cannot both be satisfied; recorded as a contract ambiguity (契约歧义). The implementation follows the more specific spec/design (`toolbar`), which is correct for `AuditToolbar`.
- [Warning] `web/src/components/data-table/column-header.tsx:39` vs `design.md:141` — the implementation adds `relative start-3 -mx-3` to the unsortable wrapper to keep the 414px row center inside the horizontal scroller, but `design.md` §4 still documents the simpler `cn('flex h-8 items-center px-3', className)`. The code comment explains the intent; the addition is not directly asserted by `column-header.test.tsx` (only height/`px-3`). Browser row-click tests at 414px (algorithms/tasks) pass, but the design/spec should be updated or the 414px behavior covered by a dedicated assertion.
- [Warning] `web/src/features/persons/index.test.tsx:121` — only persons asserts the page title; the other four pages' titles are verified by source/locale inspection, not by an automated page-level test. V5's title clause is therefore partially test-backed. Add a small `ListPage` title assertion per page if the contract is read literally.
- [Note] `web/src/components/data-table/shell.test.tsx:70-73` — container assertions use `toContain('rounded-md')` / `'border-border'` / `'bg-card'`, not exact equality; an extra class would not fail the test. Source is exact and no page passes `className`.
- [Note] `web/src/components/data-table/shell.test.tsx:144-164` — the renderRow test uses one data row and asserts 2 `tbody tr`; the contract's "two rows → 4 `tr`" case is not exercised. `shell.tsx:115-120` wraps each row's renderRow in a per-row Fragment, so the arithmetic is correct.
- [Note] `web/src/components/data-table/shell.test.tsx:77` — `td` is asserted to contain `td-extra` but not `meta.className` (`w-40`); `shell.tsx:169-172` applies both.
- [Note] Full-suite claim: the user-reported "32 files / 264 tests" is exactly shard 6/6 of `node run-browser-tests.mjs`; the full suite is 200 files / 1389 passed + 1 skipped (six shards, `--maxWorkers=1`). No failures.
- [Note] `pnpm run lint` is 0 errors / 4 warnings: 2 new (`column-header.test.tsx:20`, `shell.test.tsx:38`, `react-hooks/incompatible-library` from `useReactTable` in tests) and 2 pre-existing (`auth/init-form.tsx:120`, `login-form.tsx:55`); the claim "4 个既有 warning" is inaccurate, but the gate exits 0.
- [Note] Environment: `make frontend-check` fails at `frontend-install` with `ERR_PNPM_STORE_DIR_OPEN_OPERATION_LOCK … Read-only file system (os error 30)`; this is the sandbox's read-only pnpm store, not a code defect. All four constituent checks pass when run individually.
- [Note] One unrelated flake was observed once: `gb28181-channels-drawer.test.tsx:232` strict-mode locator found 2 "添加为摄像机" buttons in shard 1; the isolated file passed 3/3 and the shard retry passed. It is outside this change's scope and not a regression.
- [Note] `gb28181` shard isolation: `make frontend-check` cannot run; the six-shard runner is the project's supported browser-suite entry and was used because raw `vitest run` with the default worker count exhausts Chromium resources (one unrelated iframe disconnect in `render-telemetry.test.ts`).

## Commands Run (summary)

- `luoshu verify run 2026-11-23-unify-list-page-shell --json` → `ready: true`, `gatesPassed: true`, `gates: []`, `incompleteTaskIds: []`, `changedFiles: 31`.
- `cd web && node node_modules/vitest/vitest.mjs run src/components/list-page.test.tsx src/components/data-table/shell.test.tsx src/components/data-table/column-header.test.tsx src/features/persons/components/persons-table.test.tsx src/features/persons/index.test.tsx src/features/cameras/components/cameras-table.test.tsx src/features/algorithms/components/algorithms-table.test.tsx src/features/audit/components/AuditTable.test.tsx src/features/tasks/components/tasks-table.test.tsx` → 9 files, 53 tests passed.
- `cd web && node run-browser-tests.mjs --maxWorkers=1` (six shards) → 200 files, 1389 passed + 1 skipped, 0 failed.
- `cd web && node_modules/.bin/tsc -b` → pass.
- `cd web && pnpm exec eslint src/features/{persons,cameras,algorithms,audit,tasks}/index.tsx` → 0 errors.
- `cd web && node_modules/.bin/eslint .` → 0 errors, 4 warnings (2 new, 2 pre-existing).
- `cd web && node_modules/.bin/prettier --check .` → pass; `node_modules/.bin/vite build` → pass (chunk-size warning only).
- `make frontend-check` → fails at `frontend-install` with `ERR_PNPM_STORE_DIR_OPEN_OPERATION_LOCK … Read-only file system (os error 30)` (environment limitation).
- `git diff --stat 0bc6d7e...` → 21 files, +754/−942; `git status --porcelain` → 21 modified + 5 new source/test + change docs; no out-of-scope source files.

## Verdict Rationale

All V1–V8 and N1–N6 checks pass; the two Review Focus risks with behavioral impact (tasks' sibling expand row and `data-state` selected selectors) are covered by existing tests, and the `totalRows` semantics match the spec (persons/audit server total, cameras/algorithms/tasks filtered length). The remaining items are documentation/test-coverage warnings: the spec's missing grid-branch condition, the checklist's `toolbarActions` wording conflict, the design-doc drift for the `-mx-3`/`start-3` fix, and the four page-title assertions. None is a runtime defect or an out-of-scope edit, so the verdict is PASS with the warnings above; the spec wording should be corrected before archive.
