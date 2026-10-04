# Initialize Zhulong Development Specifications

## Goal

Establish practical development specifications for a greenfield, deep-learning-based NVR. Future implementation tasks must share the intended architecture and safety boundaries without mistaking planned conventions for existing code.

## Confirmed Inputs

### Owner requirements

- Product: a network video recorder with deep-learning capabilities.
- Stack: Go, CGO, C++, and React.
- Go directory architecture: organize packages by module ("模块分包"). The proposed baseline groups code by business capability inside the existing single Go module; implementation details and examples are described in `design.md` for final review.
- Database: SQLite, accessed through GORM. The owner selected Gin for HTTP handling, Fx for dependency injection and lifecycle management, and Swagger 2.0 (Swaggo) for API documentation.
- Frontend assets are embedded in the Go executable. The owner-specified stack, feature layout, and coding rules are recorded in the Frontend Requirements section below.
- HTTP API Response & Error Contract: Use a unified 3-field envelope (`code`, `message`, `data`) with string error codes and no redundant `success` boolean. Success uses `code: "OK"` (or `"ok"`) and `message: "success"`; errors use machine-readable uppercase error codes, while the backend translates `message` based on the request's `Accept-Language` header; frontend displays error messages directly via Toast notifications. Semantic HTTP status codes (2xx/4xx/5xx) are preserved; internal CGO, driver, and database errors are redacted from responses and logged safely on the server via Uber Zap.
- Deployment: option A, explicitly selected by the owner. Ship one application executable with embedded React assets; prefer linking project-owned C++ code into it, while allowing documented external system/native/hardware runtimes. This is not an all-static or dependency-free binary requirement. SQLite data and recordings remain writable external data.
- C++ implements a node-based processing pipeline, including capture, decode, preprocessing, and inference. The owner confirmed that "Pipelite" means Pipeline; a Node is a C++ processing stage, not Node.js or a distributed service.
- This task initializes specifications, not the application implementation.

### Repository evidence

- `go.mod` declares module `github.com/nikonikowuw/Zhulong` and Go `1.27.1`; the inspected local toolchain also reports Go `1.27.1` on linux/amd64. The host is not evidence of the intended deployment platform.
- There are no application source files, tests, frontend package manifest, native build files, or database migrations. `README.md` is empty.
- `.trellis/spec/backend/` contains unfilled templates. Shared guides contain upstream Trellis-specific examples and paths that do not describe Zhulong.
- `AGENTS.md` delegates workflow and specification management to `.trellis/`.
- `.gitattributes` has Git LFS rules for model-file extensions. These rules alone do not establish an inference backend, deployment chipset, or SDK selection.

## Requirements

- R1: Distinguish observed repository facts, owner-confirmed architectural requirements, and newly proposed engineering conventions.
- R2: Cover the Go service, SQLite persistence, React asset embedding, CGO boundary, and C++ inference engine at their actual responsibility boundaries.
- R3: Make native memory ownership, allocation/release pairing, error propagation, callback lifetime, thread safety, and shutdown contracts explicit. For the confirmed C++ node pipeline, cover inter-node data lifetime, bounded queues, cancellation, and worker shutdown. Do not assume one thread per node, a graph scheduler, or the same overflow policy for recording and inference.
- R4: Document the selected deployment boundary, required external native runtimes, and separation of writable data and model assets from the executable. Verify compatibility and link assumptions per target rather than promise a fully static artifact.
- R5: Replace template-only content and unrelated upstream examples with concise Zhulong-specific guidance. Keep indexes, pre-development checklists, and quality checks consistent with the resulting files.
- R6: Include verifiable commands and clearly labeled illustrative examples; do not present examples as existing application APIs or report an empty package set as passing application tests.
- R7: 规范文档全部采用中文（Chinese）编写，符合项目所有者与团队的实际沟通习惯。
- R8: Document module-oriented Go package organization, with each business capability owning its related logic, data access, boundary adapters, and tests instead of scattering them across top-level technical-layer packages. Specify acyclic dependencies and distinguish Go packages from independently versioned Go modules; do not scaffold unused packages.
- R9: Preserve the owner's selected frontend stack and TypeScript strictness; do not substitute competing tools or treat these choices as undecided.
- R10: Document the owner-specified frontend feature layout, public `index.ts` import boundary, shared-code location, and `@/` alias rule.
- R11: Carry all eleven owner-specified frontend coding rules into actionable implementation and review guidance without silently weakening their thresholds or restrictions.
- R12: Provide a concrete C++ Pipeline directory proposal with public/private header boundaries, node ownership, SDK adaptation placement, dependency direction, and test placement. Review the proposal before adopting it; do not create unused scaffolding or infer a selected SDK/build system from example paths.
- R13: Require both light and dark frontend themes, with consistent coverage of pages, shared components, overlays, forms, charts, and interaction states.
- R14: Require complete internationalization for English, Simplified Chinese, and Traditional Chinese. All frontend user-facing text must participate in i18n, not just page headings or navigation.
- R15: Use Gin for the Go HTTP layer while preserving business-module package ownership and thin transport adapters.
- R16: Use GORM for SQLite persistence; document transaction/context handling, migration discipline, and separation of persistence details from HTTP contracts.
- R17: Use Fx for dependency injection and application lifecycle management, with ordinary business constructors and explicit composition boundaries. Validate dependency graphs and actual startup/shutdown behavior, including partial-start cleanup, hook ordering, migration readiness, and native resource lifetime. Pin and verify the chosen Fx release against the project toolchain.
- R18: Require Swagger 2.0 documentation for the HTTP API generated via the Swaggo/Gin workflow (`swaggo/swag` and `gin-swagger`), with request/response/error/authentication descriptions consistent with implemented routes.
- R19: Database schema changes must use ordered, versioned migrations executed automatically by the application during startup before serving API requests or starting the Pipeline, stopping startup on failure. Record applied versions, distinguish fresh installation from upgrades, preserve user data, and do not substitute GORM `AutoMigrate` for the versioned upgrade path.
- R20: Specify the unified HTTP API response and error contract using the owner-selected 3-field schema (`code`, `message`, `data`), preserving semantic HTTP status codes, string error codes with backend i18n translation via `Accept-Language` and direct frontend Toast presentation, and internal error redaction with structured logging.

## Backend Stack Requirements

| Concern | Owner selection |
| --- | --- |
| HTTP framework | Gin |
| Persistence | GORM + SQLite |
| Schema changes | Versioned migrations executed automatically during startup; no manual end-user SQL |
| Dependency injection | Fx, including application lifecycle hooks |
| API documentation | Swagger 2.0 via Swaggo (`swaggo/swag` + `gin-swagger`) |
| Structured logging | Uber Zap (`go.uber.org/zap`) |

These are selected technologies, not installed dependencies. Exact versions and toolchain compatibility must be checked when implementation adds them. The approved Fx selection and lifecycle evidence are recorded in `research/di-comparison.md`; inspected Swagger-tool support is recorded in `research/backend-tooling.md`.

## Frontend Requirements

These requirements were supplied by the owner, not inferred from installed dependencies. No frontend package manifest exists yet.

### Selected Stack

| Concern | Required choice |
| --- | --- |
| UI and build | React 18+, TypeScript with strict mode, Vite |
| Routing | React Router |
| Server data | TanStack Query |
| Global client state | Zustand, only when genuinely necessary |
| Forms | React Hook Form + Zod |
| Styling | Tailwind CSS + shadcn/ui |
| Tests | Vitest + React Testing Library |

### Feature Layout and Imports

Relative to the frontend project root:

- Organize vertical feature slices as `src/features/<module>/{components,hooks,api,types.ts,index.ts}`.
- Cross-module references must use the owning feature's `index.ts` public exports, not internal files.
- Cross-module reusable code belongs in `src/shared`.
- Use the `@/` path alias; prohibit multi-level relative imports.

### Coding Rules

1. Use function components and Hooks only; no class components.
2. Give each component one responsibility. Split subcomponents along natural view boundaries rather than enforcing an arbitrary line count.
3. UI components focus on rendering; extract data fetching and business logic into custom Hooks.
4. Prefer composition with `children` and slot props over accumulating boolean configuration props.
5. TanStack Query owns all server data; do not put server data in global stores.
6. Keep state close to its consumers. Compute derivable values during rendering rather than synchronizing them with `useEffect`.
7. Prohibit `any`; Props must have explicit type definitions.
8. Do not add `memo`, `useMemo`, or `useCallback` preemptively. Optimize only after identifying a performance problem.
9. Use stable list keys, not array indexes.
10. Use semantic HTML, keyboard-accessible interactions, and ARIA attributes when needed.
11. Handle loading, error, and empty states for every asynchronous operation.

### Themes and Localization

- Support light and dark themes throughout the frontend, not just the page background.
- Provide complete English, Simplified Chinese, and Traditional Chinese translations. Traditional Chinese is a required locale, not an optional future translation.
- The i18n requirement covers all frontend user-facing text, including navigation, buttons, labels, placeholders, validation errors, notifications, dialogs, loading/error/empty states, tooltips, chart text, document titles, and accessible names/descriptions.
- Approved preference policy: valid saved user choices take priority and are remembered locally. Without a saved choice, theme follows system appearance and language matches browser preferences; unsupported languages fall back to English. An explicit theme selection is not overwritten by subsequent system changes.

## Out of Scope

- Implementing services, React components, C++ engine code, schemas, or migrations.
- Replacing the selected Gin/GORM/Fx stack or choosing a SQLite driver, media SDK, inference vendor, accelerator, or distributed deployment model without an actual decision.
- Changing `go.mod`, product files, Trellis runtime scripts, or agent-host configuration.
- Committing existing untracked repository content without a reviewed commit plan.

## Acceptance Criteria

- [x] Specification sections accurately cover the owner-confirmed stack and node-engine meaning.
- [x] Backend specifications preserve Gin, GORM/SQLite, Fx, and Swagger; route/module ownership, Fx graph and lifecycle tests, cleanup responsibilities, and Swagger generation checks are explicit.
- [x] Database guidance requires versioned migration history and program-managed execution rather than user-run SQL or GORM `AutoMigrate`; fresh-install, upgrade, repeated-run, failure, and incompatible-version checks are specified.
- [x] Go layout guidance uses module-oriented packages, shows an explicitly proposed directory example, and avoids mandatory per-layer or per-feature `go.mod` scaffolding.
- [x] Native directory guidance includes an explicitly proposed layout, C ABI versus private C++ boundaries, per-node organization, and acyclic dependencies without selecting a vendor SDK or a general DAG scheduler.
- [x] Frontend specifications preserve the complete selected stack, feature import boundaries, and all eleven coding rules, including the single-responsibility split principle and no-`any` rule.
- [x] Theme specifications cover both light and dark appearances, including overlays, interaction states, readability checks, and the approved saved-preference/system-default behavior.
- [x] Localization specifications require all three languages, complete UI-text coverage, missing-translation detection, locale-aware formatting, and checks across the six theme/language combinations.
- [x] Existing facts and initialization conventions are distinguishable, with links to real repository evidence where applicable.
- [x] Cross-language safety and deployment constraints are explicit and testable without fabricated SDK APIs.
- [x] No unfilled templates, unrelated upstream-source instructions, or broken local links remain in `.trellis/spec/`.
- [x] Indexes enumerate the actual specification files and include pre-development and verification guidance.
- [x] Database guidance requires versioned migration history and startup-managed automatic execution before serving traffic, with failure halting startup.
- [x] Swagger specifications define Swagger 2.0 generation using the Swaggo/Gin workflow (`swaggo/swag` and `gin-swagger`).
- [x] Unified HTTP response and error handling specifications cover the 3-field schema (`code`, `message`, `data`), semantic HTTP status codes, internal error redaction via Uber Zap, backend i18n translation via `Accept-Language`, and direct frontend Toast presentation.
- [x] Documentation checks and any example checks are recorded separately from application tests, which currently have no packages to run.
- [x] The final plan is reviewed before specification implementation; completion and commit/archive status are reported accurately.

## Deferred Technical Selections

The selected frontend and backend stacks remain fixed; exact compatible dependency/generator versions, the i18n library, and the package manager/lockfile strategy remain to be pinned during implementation (React must be 18 or newer). Gin/GORM/Fx toolchain compatibility, the SQLite version/driver and migration library, C++ standard/build system, target operating systems and architectures, media/inference SDK versions, and model packaging also need concrete verification during their corresponding implementation planning. No installed dependencies or runtime compatibility are implied by the empty repository.
