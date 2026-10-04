# Specification Bootstrap Execution Plan

## Planning and Activation Gates

- [x] Inspect the repository and existing specifications.
- [x] Confirm C++ processing-node Pipeline meaning, deployment option A, module-oriented Go packages, and the owner's frontend stack/layout/eleven coding rules.
- [x] Capture the additional light/dark theme and complete English/Simplified Chinese/Traditional Chinese requirements.
- [x] Resolve the initial theme/language preference policy and reconverge `prd.md` and `design.md` for final review.
- [x] Capture Gin, GORM/SQLite, Fx, and Swagger as owner-selected backend requirements; inspect upstream tooling/lifecycle evidence. The owner explicitly replaced the earlier Wire choice with Fx.
- [x] Capture versioned migrations without manual end-user SQL as a database requirement.
- [x] Confirm startup migration execution timing, Swagger 2.0 (Swaggo) format, and the unified 3-field response contract, then reconverge the complete plan.
- [x] Validate the planning artifacts and context manifests.
- [ ] Present the final summary and receive owner approval.
- [ ] Activate the existing task with `task.py start`; verify the session-scoped active task. The record is intentionally `planning` until then. If session identity is unavailable, follow the CLI's supported identity setup instead of fabricating a pointer or modifying runtime scripts.

## Ordered Work

1. [x] Refresh the current file inventory and take a pre-edit snapshot. Load the bootstrap writing guidance, layer-specific guidance relevant to the examples, and `trellis-before-dev`. Read current files again before overwriting templates.
2. [x] Write the root specification index and architecture document. Separate existing facts, approved conventions, and deferred dependency selections.
3. [x] Replace the six backend templates with concrete Gin/GORM/SQLite guidance and add HTTP API/Swagger 2.0 (Swaggo) and Fx dependency-injection/lifecycle guidelines. Preserve business-module ownership and colocated runtime handlers/SQL/tests; centrally order and embed versioned schema migrations with startup-managed execution. Document fresh-install/upgrade/failure handling without manual end-user SQL or GORM `AutoMigrate`, along with DTO boundaries, the unified 3-field response contract (`code`, `message`, `data`), Swaggo 2.0 artifact checks, Fx graph validation, lifecycle ordering, partial-start cleanup, and native timeout safety. Do not add DI code generation or claim Fx/Go compatibility without verification. Keep commands accurate for the current empty package set.
4. [x] Add the native index, directory structure, CGO contract, and Node Pipeline guidelines. Mark the directory tree as a reviewed proposal rather than existing source; explain the C-only public header, private node directories, SDK adapter boundaries, test placement, and acyclic dependencies. Include ownership, exception containment, callbacks, bounded queues, cancellation, and join-before-release test expectations without selecting an SDK or build system.
5. [x] Add the frontend index, directory structure, development guidelines, theme/i18n contract, and quality guidelines. Preserve the selected stack and all eleven owner rules, including feature public exports, shared-code placement, strict TypeScript/no `any`, single-responsibility component splitting, and evidence-driven memoization. Cover both themes, full three-language UI text coverage, approved preference behavior, catalog completeness, alias consistency, Vitest/React Testing Library checks, and the Vite-to-Go embedded-asset build without inventing package scripts or silently selecting an i18n dependency.
6. [x] Rewrite shared guides into short Zhulong-specific checklists, removing copied upstream paths, duplicate sections, TypeScript-specific upstream examples, and unverifiable numerical claims.
7. [x] Run the validation below and review with `trellis-check`. Apply the `simplify` skill if executable examples were written. Fix local issues and rerun affected checks; do not silently widen scope.
8. [x] Perform the `trellis-update-spec` consistency pass: each cross-layer rule has one owner, indexes link to it, and deferred choices are consistent throughout the documents.
9. [x] Record verification results in this task and update acceptance checkboxes only for verified work.
10. [ ] Present a scoped commit plan separately from unrelated untracked content. Do not commit, archive, or record a completion journal before the required owner confirmation and workflow gates.

## Validation

Run commands from the repository root. Dependencies for documentation checks are Python 3 and standard shell tools; Go is needed only for toolchain and future application checks.

```bash
python3 ./.trellis/scripts/task.py validate 00-bootstrap-guidelines
python3 ./.trellis/scripts/get_context.py --mode packages
go version
go env CGO_ENABLED GOOS GOARCH
```

Additional checks to execute and record:

- Use Python standard-library checks for relative Markdown links, index coverage of sibling guideline documents, required index checklists, unfilled-template markers, empty headings, and trailing whitespace. All relative link targets must exist; future source paths are labeled prose, not fake links.
- Search the finished specs for upstream-only paths such as `src/templates/`, `packages/cli/`, and `docs-site/`; none should remain as Zhulong instructions. Check for claims that Node means Node.js or a distributed service.
- Validate each new code sample according to its declared dependencies. Compile/run self-contained Go/C/C++ examples in temporary directories; record unexecuted environment-dependent commands as such. Do not claim cross-language linking or hardware validation from a standalone compile.
- Check Go package availability explicitly. With zero application packages, report application test/build/vet checks as not applicable; do not turn `no packages to test` into a passing application suite. Run ordinary Go test/vet/build gates when real application packages exist.
- Compare the final specs against the pre-edit snapshot and the file table in `design.md`; verify protected-file checksums, task JSON/JSONL validity, and absence of accidental source files or build artifacts.
- Check the frontend stack and all eleven owner rules against `prd.md` individually. Ensure generic React performance guidance does not replace TanStack Query with SWR, bypass public feature exports, add speculative memoization, or require React-19-only APIs. Distinguish strict TypeScript checks from explicit-`any` linting and Vite transpilation.
- Check the native directory proposal: public headers expose only the C ABI; pipeline contracts do not import concrete nodes or SDKs; backend adapters do not depend on nodes/ABI; node tests have a hardware-independent path. No example directory implies a required vendor backend, build system, plugin registry, or general DAG scheduler.
- Check theme/i18n requirements: English, Simplified Chinese, and Traditional Chinese are all mandatory; every app-authored visible/accessible string is localized; locale fallback does not waive missing translations; shared tokens cover both themes and portals. Record a future six-combination UI test matrix without claiming rendered UI validation in this empty repository.
- Check Gin/GORM/Fx/Swagger coverage against the owner requirements, `research/di-comparison.md`, and `research/backend-tooling.md`. Ensure Fx providers are described as demand-driven and hooks as append/reverse-append ordered; graph checks do not substitute for runtime lifecycle tests, and timeout does not imply native cancellation. Do not carry forward the superseded DI generation workflow. Preserve SQLite-specific constraints and do not claim Swagger 2.0 examples as OpenAPI 3.x evidence. Future application checks remain unexecuted until real code and pinned dependencies exist.
- Check versioned migration requirements: migration history, embedded sources, immutable released versions, one ordered database-wide sequence, compatible version ranges, repeated-run no-op behavior, failure/interruption handling, concurrency protection, and safe backup/restore. Startup execution must remain labeled as proposed until approved. No actual migration or recovery test can be claimed in this empty repository.
- Review requirement coverage R1–R19, including versioned migrations without manual end-user SQL, the selected backend stack, module-oriented Go packages, the native directory proposal, the owner's frontend/theme/i18n requirements, and single-application-file deployment with external runtimes and mutable data, rather than all-static deployment.

## Rollback Points

- Before specification writes: revert only this task's planning edits if requested; keep owner input and unrelated changes intact.
- During writing: use the latest pre-edit snapshot for affected documents only, checking for subsequent owner edits before restoration.
- If a design decision needs owner input: return to planning, update the affected artifacts, and obtain a fresh approval rather than changing the architecture implicitly.
