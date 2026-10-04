# Zhulong Specification Bootstrap Design

## Status and Scope

Planning includes the owner-approved switch to Fx for dependency injection/lifecycle management and mandatory versioned migrations without manual end-user SQL. The rationale and lifecycle evidence are in `research/di-comparison.md`. Migration execution timing and the Swagger specification format still need confirmation before the revised final complete execution summary. This document designs the specification set, not a shipped implementation. Confirmed requirements live in `prd.md`; observed evidence lives in `research/`.

Use one owner for analysis, writing, and verification, as requested by the bootstrap skill. Do not introduce extra agents, repositories, service processes, or a monorepo package configuration for this documentation task.

## Intended Responsibility Boundaries

| Area | Initial convention to document | Explicit boundary |
| --- | --- | --- |
| Go | Fx-composed application lifecycle, Gin HTTP handlers, GORM/SQLite access, embedded frontend serving | Do not route every decoded frame through Go just to orchestrate C++ nodes |
| CGO / C ABI | Small bridge with opaque native handles, bounded calls, explicit status and ownership | No exposed C++ ABI, cross-boundary exceptions, retained unpinned Go buffers, or implicit callback ownership |
| C++ | Capture, decode, preprocess, inference nodes and their queues/resources | No browser concerns or direct application database ownership by default |
| React | Browser interface consuming the Go service contract | No direct SQLite access, native-pointer exposure, or production Node.js server requirement |
| Deployment | One application executable, embedded web assets, documented external native runtimes | Mutable SQLite/recordings are not embedded; all-static linking is not promised |

These are proposed greenfield conventions for approval, not claims of existing packages. The exact media transport, frontend API protocol, engine SDK, and hardware platform remain intentionally unselected.

## Go Package Organization

The owner requested module-oriented packages. Interpret this as business-capability packages within the existing Go module, not a separate `go.mod` per capability. This interpretation and the illustrative layout below are part of the final review.

Proposed placement only; do not create these paths during specification bootstrap, and add each package only when its feature needs it:

```text
cmd/Zhulong/main.go          # Thin executable entry point
internal/
  app/                    # Composition and application lifecycle
    app.go                # Fx application/root composition
    providers.go          # Provider registration and bindings, split only as needed
    lifecycle.go          # Explicit startup/shutdown coordination, split as needed
  camera/                 # Camera management capability
    camera.go             # Module types and behavior
    handler.go            # Module's API adapter, if needed
    store.go              # Module-owned persistence, if needed
    camera_test.go        # Tests colocated with the module
  recording/              # Recording management capability
  inference/              # Inference task/configuration management
  engine/                 # Go/CGO bridge, not business policy
  database/               # Shared SQLite connection and versioned migration infrastructure
    migrations/           # Ordered migration sources embedded in this Go package
  webui/                  # Go asset embedding/serving package
native/                   # C++ pipeline and C ABI implementation
web/                      # React source and build project
```

- Do not create top-level `internal/handlers`, `internal/services`, or `internal/repositories` as the primary organization. A module may separate concerns by files first; add subpackages only for real size or dependency boundaries, not a mandatory layered template.
- Business modules own their runtime SQL and domain behavior. Shared database utilities do not become a global repository for business logic; versioned schema changes are centrally ordered under `internal/database/migrations/`, with their owning module identified.
- `app` wires dependencies. Modules do not import `app`. Lower-level utilities and the CGO bridge do not import business modules.
- Cross-module imports must be acyclic and use deliberate exported APIs. Use small consumer-owned interfaces where substitution or decoupling is needed; do not manufacture an interface for every struct.
- Avoid global service locators, catch-all `utils`/`common`/`models` packages, and a speculative public `pkg/` tree. Shared code needs a concrete owner and actual consumers.
- Go-facing engine interfaces must not leak C/C++ types into business packages. Native allocation/release remains paired on the native side; the Go bridge coordinates handle lifetime.

## Gin, GORM, Fx, and Swagger Contracts

These technologies are explicit owner choices. Keep ordinary module constructors and ownership boundaries; the selected framework/tooling must not turn the module-oriented layout into global handler/service/repository packages.

### Gin Transport Boundary

- Keep each business module's Gin handlers and route-registration entry points with that module. `app` composes route groups, middleware, and dependencies rather than owning every handler.
- Bind and validate HTTP input at the handler boundary, map it to explicit module inputs, and pass `context.Context` into request-scoped work. Do not pass `*gin.Context` into stores, native adapters, or long-lived workers; persistent engine tasks need an application-owned lifetime rather than accidental cancellation at HTTP request return.
- Use deliberate request/response DTOs for the external API; do not expose GORM association graphs, native objects, or persistence-only fields as the HTTP contract.
- Unified response envelope: All HTTP JSON endpoints use the owner-approved 3-field envelope `{ code, message, data, details? }` without a redundant `success` boolean.
  - Success: HTTP 200/201, `code: "ok"`, `message: "success"`, and the payload in `data`.
  - Failure: HTTP 4xx/5xx, `code: "<semantic_error_enum>"` (e.g. `CAMERA_NOT_FOUND`), developer message in `message`, optional `details` for field-level validation errors (HTTP 422), and omitted/null `data`.
  - Error redaction: Internal errors (CGO crashes, SQLite locks, low-level driver failures) are recorded on `AppError.Err` (`json:"-"`) and logged via Uber Zap (`zap.L().Error`) on the server; they are never exposed to HTTP clients.
  - Backend i18n & Frontend Toast: The React frontend passes the user's active locale in the `Accept-Language` header; the backend translates `message` accordingly; the frontend directly displays `error.message` via a Toast notification component (`toast.error(error.message)`), eliminating redundant error code dictionaries on the client.
- Register API, documentation, and embedded SPA routes without allowing the frontend fallback to disguise an API/documentation/asset failure as successful HTML. No final URL prefix or authentication scheme is selected in this task.

### GORM with SQLite

- Module stores own their GORM queries and persistence mappings. Shared database infrastructure opens/configures the database and exposes lifecycle handling; it does not become a global business repository.
- Use context-bound database operations, explicit transactions for atomic changes, and inspect errors and affected-row behavior. Distinguish not-found from operational failure; do not leak raw SQL/driver errors to clients.
- Preserve SQLite-specific rules for bounded write contention, connection-level PRAGMAs, file permissions, and coherent backups. GORM does not eliminate SQLite's locking constraints.
- Schema upgrades must use the owner-required versioned migration runner, not GORM `AutoMigrate` or instructions for users to run SQL manually. Keep migration history distinct from ORM model declarations; the lifecycle contract follows below.
- The concrete GORM SQLite driver and versions are still unselected. Verify their CGO/linking behavior against deployment option A; do not claim a pure-Go or all-static artifact by inference.

### Versioned Database Migration Contract

The owner requires versioned migrations without manual end-user SQL. SQL files maintained by developers remain valid migration sources; the application/migration runner executes them, rather than asking users to paste statements into SQLite.

- Place ordered migration sources under the proposed `internal/database/migrations/` and embed them into the application using the selected runner's supported embedded-source integration. Database files stay external; normal installation and upgrades must not require distributing or manually executing loose SQL files.
- Use one database-wide ordered version history even though business code is module-oriented. Each migration identifies its owning module; changes crossing tables/modules have one coordinated ordering rather than independent, racing module startup migrations.
- Record applied versions using the chosen migration tool's metadata. The tool and its metadata schema are not selected here; do not invent a version table name or claim built-in checksums/dirty-state behavior that has not been verified.
- Released migrations are immutable. Add a subsequent version for a change or fix rather than editing a script that some installations already applied. Apply only pending versions; restarting after success is a no-op, not another execution of every SQL file.
- Validate current schema history against the binary's supported range. A database newer than the executable, an unknown baseline, or an unsupported migration gap must not trigger an automatic downgrade, forced version marking, or destructive reinitialization.
- Serialize migration ownership across processes for the version-check/apply interval. Do not rely on a process-local mutex or silently allow two application instances to race a schema upgrade.
- Use transactional schema/data updates where the selected SQLite operations and runner support them, recording successful application consistently with the change. Operations that cannot be transactional require a documented interruption/recovery strategy; do not promise rollback of every possible migration.
- Define and test a consistent backup/restore path before destructive or nontransactional changes. Under WAL mode, blindly copying only the live database file is not a valid backup strategy. Disk-full, read-only storage, interrupted migration, and lock contention must surface actionable diagnostics without proceeding against an uncertain schema.
- Recovery uses a validated migration/recovery procedure or consistent backup restoration; users are not expected to manually edit schema/version tables. Do not automatically run destructive down migrations after a failure or binary downgrade.

Approved startup integration:

```text
Open/configure database
  -> acquire migration ownership and check version history
  -> apply pending versions and verify success
  -> release migration ownership
  -> finish application initialization
  -> serve API requests and start Pipeline workers
```

A failed migration or incompatible schema prevents normal startup under this approved policy. Fx constructors/invocations must not start camera/Pipeline workers or expose normal HTTP traffic before this gate succeeds. Migration and startup timeouts must be deliberate; moving migrations to the background does not satisfy the readiness gate. This favors predictable, self-upgrading single-file deployment over a separate operator-run migration command, at the cost of upgrade work delaying startup. Database connection cleanup still runs when initialization fails.

Future tests must cover fresh database initialization, each supported upgrade path, data preservation, repeated successful startup, failed/interrupted application, version mismatch, concurrent starts, and restore behavior. This task writes those contracts only; it does not create an initial schema, choose a migration library, or run database migrations.

### Fx Composition and Lifecycle

- Keep application-wide Fx options, provider registration, and lifecycle adapters at the `internal/app` composition boundary. Business modules retain ordinary constructors and methods; handlers do not receive a container or resolve dependencies during requests.
- Fx is a runtime dependency graph and lifecycle library linked into the executable, not a code generator or separate production service. No DI-generated source files or generator-only build tags are required.
- Providers are demand-driven: registration alone does not instantiate a service or run its hooks. The application root must explicitly consume every required service; provider argument order is not a startup-order contract.
- Constructors should compose inert components, not start goroutines or acquire resources that would leak if graph initialization fails. Acquire live resources in managed startup phases where possible, and require a failing constructor/start hook to release its own partial resources. Do not assume a stop hook will run for a component that did not start successfully.
- `OnStart` runs in hook append order; `OnStop` runs in reverse append order. Encode dependencies deliberately, or use an application coordinator for multi-phase shutdown. A DI graph alone does not prove the required database/migration/HTTP/Pipeline lifecycle order.
- Lifecycle hooks perform bounded startup/readiness and stop/join work. Permanent workers run with application-owned lifetime/cancellation, not the startup-hook context. Shutdown must prevent new work, quiesce HTTP users and workers in the required order, stop native callbacks, release engine resources, and close SQLite only after its users stop.
- Respect and test hook deadlines, but never treat a timeout as proof that blocking CGO/native work has ended. C++ must provide stop/unblock/join semantics, and native memory must not be freed while still in use.
- Pin a compatible Fx release and verify it with the project's Go 1.27.1 toolchain. Check graph construction as well as real start/stop, migration failure, partially failed initialization, repeated cleanup, and background-worker shutdown. A graph-only check is not proof that runtime resources start or stop correctly. The inspected lifecycle evidence and limits are in `research/di-comparison.md`.

### Swagger 2.0 Documentation via Swaggo

- The owner selected Swagger 2.0 documentation generated via the Swaggo/Gin workflow (`swaggo/swag` and `gin-swagger`). Document Gin HTTP routes with their parameters, DTO schemas, success and error responses (using the unified 3-field envelope), security requirements where applicable, and examples without real credentials or private camera URLs. Document native C ABI behavior separately, not as Swagger endpoints.
- Keep API annotations next to module handlers/DTOs when using code-first generation; generated output must match the implementation rather than become a separately hand-edited contract. A future task can select contract-first generation if the required format warrants it.
- Keep generated Go documentation registration under the proposed `internal/apidocs/` when required by the chosen generator, with schema artifacts available for validation and embedding. This is a tooling module, not a replacement for business module ownership.
- Generator versions, source scanning of `internal/` packages, clean regeneration, and route/schema consistency are explicit build checks once the routes exist. Do not prescribe unverified CLI flags or an `@latest` tool installation as the reproducible build.
- Interactive documentation exposes API details and may offer live requests. Its eventual route/exposure policy must be explicit and tested, not silently public. The documentation UI must not bypass the owner's theme/i18n requirements if it is exposed as part of the end-user frontend; do not assume an unlocalized third-party UI is an approved exception.

## Native Pipeline Directory Proposal

The owner requested the Pipeline directory architecture. The following is a proposed layout for final review, not an existing file set or a mandate to create every directory up front. Use `.h` for the C-compatible public header and `.hpp`/`.cpp` for private C++ declarations/implementation.

```text
native/
  include/Zhulong/
    engine.h                  # Public C ABI only
  src/
    abi/
      engine.cpp              # C ABI validation, handle bridge, exception containment
    pipeline/
      node.hpp                # Common processing-node contract
      pipeline.hpp
      pipeline.cpp            # Assembly support and lifecycle coordination
      packet.hpp              # Encoded media and its timing/ownership metadata
      frame.hpp               # Decoded frame metadata and buffer ownership
      result.hpp              # SDK-neutral processing results
      bounded_queue.hpp       # Bounded transport with cancellable waits
    nodes/
      capture/
        capture_node.hpp
        capture_node.cpp
      decode/
      preprocess/
      inference/
    backends/                 # Add only for concrete SDK integrations
  tests/
    abi/
    pipeline/
    nodes/
```

Each implemented node owns its header, implementation, node-specific settings, and resource management inside its directory. Add recording, postprocessing, or other node directories only with their actual feature requirements. Packet, frame, and result are distinct data concepts; the example filenames do not require a generic message bus or a fully generic payload variant.

### Responsibility and Dependency Rules

- `include/Zhulong/engine.h` is the only intended native header surface for Go/CGO. It must be consumable as C, with C++ linkage guards, opaque handles, and explicitly owned inputs/outputs. No STL, C++ classes, or vendor headers escape into it.
- `src/abi` implements that boundary and acts as the entry-level composition site for the initial small engine. It may construct concrete nodes and assemble a pipeline, but must not contain decode/preprocessing/inference algorithms. Extract a separate assembly unit only when real complexity warrants it.
- `src/pipeline` owns common node/data/queue contracts and lifecycle coordination. It must not include concrete node implementations, the C ABI adapter, or vendor SDK headers. It does not imply one worker per node, a dynamic plugin registry, a general DAG scheduler, or lock-free queues.
- `src/nodes` implements processing stages using pipeline contracts. Nodes communicate through explicit input/output boundaries instead of calling another node's implementation or sharing mutable peer state.
- `src/backends` isolates concrete SDK/runtime integration when added. It may depend on SDK-neutral data contracts, but must not depend on concrete nodes or the ABI layer. Vendor objects and destruction rules remain private to their adapter. Do not create empty per-vendor directories or an abstraction for hypothetical future backends.
- Dependency direction is acyclic: `abi -> nodes/pipeline`; `nodes -> pipeline/backends`; `backends -> SDK-neutral data contracts`. No native directory imports Go business logic or owns application SQLite persistence.
- Tests mirror responsibilities: ABI validation and destroy behavior, queue cancellation and pipeline lifecycle, and individual node behavior. Node/queue tests should use synthetic data or narrow fakes without requiring cameras or accelerators; actual backend tests remain separate integration tests with declared runtime prerequisites.

### Ownership and Shutdown Consequences

C++ allocates and releases native buffers/objects; Go coordinates opaque handle lifetime through paired C ABI calls. Queue transfer or sharing must explicitly preserve payload ownership, and fan-out must not introduce mutable buffer races. A backend resource's deleter and runtime must remain alive until the last borrower releases it. Stop producers, unblock waits, apply the chosen drain/discard behavior, quiesce workers and callbacks, then destroy native resources. SDK-specific release requirements take precedence over merely freeing a wrapper allocation.

This directory proposal does not choose a compiler, C++ standard, build system, media SDK, inference vendor, or model packaging strategy.

## Frontend Organization and Contract Mapping

The owner-selected frontend stack and eleven coding rules in `prd.md` are binding inputs, not optional defaults. Use React 18+ with strict TypeScript and Vite; no Next.js or production Node.js server is implied. Exact compatible package versions are selected when implementation adds the package manifest.

Map the owner's `src/` convention under the proposed `web/` frontend root:

```text
web/src/
  features/
    camera/                # Example feature, not an implemented module
      components/
      hooks/
      api/
      types.ts
      index.ts             # Public feature exports
  shared/                  # Actual cross-feature reuse
```

- Cross-feature imports use the owning public entry point, such as `@/features/camera`; deep imports such as `@/features/camera/hooks/useCamera` from a different feature are forbidden. Internal feature files must not import their own public barrel in a way that creates a cycle.
- Configure `@/` consistently for TypeScript, Vite, and Vitest. TypeScript path mapping alone does not guarantee bundler resolution. For shared shadcn/ui primitives, align its generator configuration with the shared-code location rather than introducing a competing global component tree.
- Keep shared modules independent of feature implementations. Public feature exports are deliberate, small, and avoid unnecessary side effects. Generic advice to bypass barrel exports must not override the owner's module boundary; investigate bundle behavior without deep-importing feature internals.
- Feature `api` code owns typed request functions, custom Hooks own query/mutation orchestration and business logic, and components render their results. React Router composes navigation without taking ownership of a second server-data cache.
- TanStack Query is the server-data source of truth. Zustand is opt-in for genuinely shared client-only state, never a mirror of query data. Forms use React Hook Form and Zod rather than another global state mechanism.
- Preserve all eleven coding rules verbatim in meaning. Component splitting follows single-responsibility and natural view boundaries rather than an arbitrary line count, avoiding compressed formatting or empty abstraction layers.
- TypeScript strict mode does not itself prohibit explicit `any`; the initial code-quality contract must require a separate lint/review check. Treat external data as `unknown` and validate it at the boundary rather than asserting away uncertainty.
- Do not apply generic recommendations for SWR, automatic memoization, React-19-only APIs, or Next.js server behavior to this React-18+-compatible Vite baseline.
- Async UI tests must distinguish loading, errors, successful empty data, and populated results; do not show an empty state merely because an initial request has no data yet. For operations without a result collection, specify the relevant no-data/no-action state rather than inventing list semantics.
- Test with Vitest and React Testing Library: user-visible behavior, keyboard operation, form validation, query isolation, subscription cleanup, and module-boundary/type checks. Vite transpilation is not a TypeScript type check; the later build must run an explicit type-check step as well.

## Theme and Internationalization Contract Proposal

Light/dark support, complete English/Simplified Chinese/Traditional Chinese localization, and the initial-preference behavior are owner-approved requirements. The implementation mechanisms below remain a proposed design, not existing code or installed dependencies.

### Placement and Theme Coverage

- Put shared theme infrastructure under the proposed `web/src/shared/theme/` and i18n initialization/catalog infrastructure under `web/src/shared/i18n/`. These stay independent of feature implementations and are composed at the application root.
- Use semantic CSS variables compatible with the selected Tailwind CSS and shadcn/ui versions for backgrounds, foregrounds, surfaces, borders, focus rings, and chart/status colors. Do not scatter fixed light-only/dark-only palette choices across feature components.
- Ensure dialogs, menus, tooltips, toasts, popovers, and other portaled UI inherit the selected theme. Verify hover, selected, focus, disabled, loading, and error states in both appearances.
- Check contrast in both modes, including normal text at 4.5:1 where applicable; color alone must not convey recording/alert status. Palette selection and rendered accessibility checks require actual UI later, not a claim of completion from this documentation task.
- Apply resolved theme before the main UI paints where feasible. Respect an explicit preference instead of overwriting it with an unconditional effect on mount. A storage-access failure must not prevent the app from starting.

### Locale and Text Coverage

- Proposed locale identifiers are `en`, `zh-Hans`, and `zh-Hant`, distinguishing writing systems rather than equating every language with one territory. The eventual resolver must handle script and regional browser tags deliberately; no unsupported-code cast to a supported locale.
- `i18next` plus `react-i18next` is a candidate, not an approved installed dependency. Keep the specification's required behavior library-independent until implementation selects and pins compatible versions.
- Group translation catalogs by locale and feature namespace, with common UI strings in a shared namespace. Bundle all three catalogs into the frontend release assets served by Go; do not require an external translation service for ordinary UI rendering.
- Cover all application-authored visible and accessible strings: navigation, actions, field labels/placeholders, Zod/RHF validation, dialogs/toasts, async states, tooltips, legends/axes, document titles, aria-labels, and image descriptions. Localize configurable third-party component text as well.
- Scope interpretation to present in review: user-entered camera names, addresses, IDs, and other raw data retain their original values; labels and system-generated messages around them are translated. Do not machine-translate user data or change identifiers as a side effect of switching the interface language.
- Translate controlled backend statuses/error codes through stable UI mappings with a localized unknown-error fallback. Do not show raw SDK or backend `error.message` strings as user-facing notifications; keep technical diagnostics in appropriately redacted logs. This does not select an API envelope or invent final error codes.
- Use full messages with named interpolation and proper plural handling rather than concatenating translated fragments. Compare semantic message coverage and interpolation parameters across locales; plural suffixes may legitimately differ by language.
- Use locale-aware `Intl` formatting for dates, numbers, and units. Locale changes must not alter event instants, stored values, or the separately defined recording timezone semantics.
- Recompute locale-dependent validation/display messages when language changes. Do not freeze translated strings at module initialization or cache language-dependent rendered messages as server data.
- Missing translations must fail review/build checks for any required locale. Runtime fallback may protect the UI but is not evidence that a locale is complete. Simplified-to-Traditional character conversion alone is not translation acceptance.

### Approved Initial Preference Policy

- Explicit, valid user-selected values take priority and are remembered in local browser storage. No account synchronization or SQLite preference schema is implied.
- Without a saved theme choice, follow system appearance; explicit light/dark selection stops system changes from overriding it.
- Without a saved language choice, match ordered browser preferences to the supported locales. Unsupported languages fall back to English. Matching of ambiguous Chinese tags must be defined and tested when implementing the resolver, rather than relying on an incidental library default.
- Provide explicit language/theme controls. Theme and locale state are client preferences, not TanStack Query server data, and do not by themselves justify a new global Zustand store.

### Verification Contract

- Use Vitest/React Testing Library to cover theme/locale selection behavior, the approved preference precedence, language-change rerenders, localized validation, accessible labels, and loading/error/empty/populated states.
- Audit all three catalogs for semantic key coverage, required plural forms, interpolation compatibility, and untranslated defaults. Add a lint/static check for hard-coded app-authored UI strings plus manual review; scanning only Chinese characters cannot detect hard-coded English.
- Verify representative screens and portaled content in all six combinations: two themes multiplied by three locales. Inspect English expansion, Chinese text wrapping, focus visibility, contrast, and date/number formatting.
- Test missing/invalid saved preferences and unavailable browser storage without treating a runtime fallback as a completed translation. The embedded production build must include every required locale and support direct-route loading in each locale.

## Data and Lifecycle Contracts to Cover

- Control travels from the browser to Go and through the C ABI to the C++ engine. Return bounded status/results rather than native object representations.
- The high-rate media path stays within the native pipeline by default. Do not force the recording path through inference sampling or allow inference overload to silently corrupt recording behavior. Recording topology and retention policy belong to later feature design.
- A Node is a processing stage; it need not own a thread. Document queue capacity, overflow behavior, time/sequence metadata, buffer lifetime, and cancellation for each actual edge when implemented.
- Native buffers are allocated and released by native code through paired APIs; Go owns wrapper state and release coordination. Synchronous borrowed buffers cannot outlive the call. The initial baseline uses native-owned storage for asynchronous work.
- Define safe stop semantics: prevent new work, unblock waits, execute the chosen drain/discard policy, quiesce workers and callbacks, then release handles and buffers. Teardown must not free resources still used by another node or callback.
- SQLite guidance covers short transactions, bounded contention, per-connection configuration, migrations, and consistent backups. WAL is a deployment-dependent choice, not a substitute for a writer-concurrency policy or a promise that copying one live database file is safe.
- Embedded-asset guidance covers build ordering, embed package containment, missing-assets build failure, route separation, and tests distinguishing SPA navigation from API/asset failures. Do not require a frontend development server in production.
- Logs and outward-facing errors must not disclose camera credentials, tokens, full credential-bearing stream URLs, or raw native memory.

## Specification Layout

Keep the existing backend filenames so current navigation can be repaired rather than replaced wholesale. Add layers only for the confirmed native and frontend responsibilities.

| Path under `.trellis/spec/` | Responsibility |
| --- | --- |
| `index.md` | Project-wide navigation and when to load each layer |
| `architecture.md` | Confirmed stack, initialization status, cross-layer ownership, deployment option A, deferred selections |
| `backend/index.md` | Backend pre-development and quality checklists |
| `backend/directory-structure.md` | Existing layout versus module-oriented Go package placement, module ownership and acyclic dependencies; create directories only when needed |
| `backend/database-guidelines.md` | Module-owned GORM/SQLite queries, embedded versioned migrations, startup gating, transactions, contention and backup/restore safety |
| `backend/http-api-guidelines.md` | Gin handlers, DTOs, unified 3-field response/error contract, Swaggo/Swagger 2.0 generation and route separation |
| `backend/dependency-injection.md` | Fx composition, graph validation, lifecycle ordering, startup failures, timeouts and safe resource cleanup |
| `backend/error-handling.md` | Go error propagation, AppError boundary mapping, internal error redaction, and unified 3-field response contract |
| `backend/logging-guidelines.md` | Initial structured-logging convention, redaction and event ownership |
| `backend/quality-guidelines.md` | Formatting, tests, race checks and accurate empty-repository validation reporting |
| `native/index.md` | Native pre-development checklist, version checks and quality gates |
| `native/directory-structure.md` | C ABI/public headers, private pipeline contracts, processing-node directories, SDK adapters and test placement |
| `native/cgo-contract.md` | Both sides of the C ABI: memory, handle, callback, error and shutdown ownership |
| `native/pipeline-guidelines.md` | Node responsibilities, data contracts, bounded queues and lifecycle tests |
| `frontend/index.md` | Selected frontend stack, pre-development and quality checklists |
| `frontend/directory-structure.md` | Feature slices, public `index.ts` boundaries, `shared` ownership, alias resolution |
| `frontend/development-guidelines.md` | All eleven owner rules, query/client/form state ownership, accessibility and async UI behavior |
| `frontend/theme-and-i18n.md` | Light/dark themes, full three-language coverage, client preferences, text/error mapping and formatting contracts |
| `frontend/quality-guidelines.md` | Vitest/React Testing Library, strict typing and no-`any` checks, six theme/locale combinations, catalog completeness, Vite build and embedded-asset verification |
| `guides/index.md` | Short guide navigation, not another source of coding contracts |
| `guides/code-reuse-thinking-guide.md` | Search real application code before sharing abstractions; no upstream Trellis registration instructions |
| `guides/cross-layer-thinking-guide.md` | Zhulong-specific boundary questions linking to the owning contracts |

Use the proposed module-oriented map above as the directory example, clearly labeled as not yet created. Frontend assets must eventually be staged inside the embedding Go package; `go:embed` cannot reach outside it with `..`.

## Evidence and Convention Policy

- Existing facts cite real repository files. Owner decisions trace to the task PRD, including the complete frontend requirements; keep them distinct from proposed defaults.
- New defaults are labeled initialization conventions. Standard-library Go errors and structured logging can establish a baseline without implying existing implementation examples.
- Examples must be small, SDK-neutral, and labeled illustrative. Validate self-contained examples in temporary directories, never by adding dummy product packages.
- For cross-layer contract sections, include scope, illustrative signatures or command forms, ownership/validation behavior, success and failure cases, test assertions, and wrong-versus-correct examples. Do not fabricate vendor functions or final application API names.
- Unselected versions are explicit future implementation prerequisites, not blank headings. Each later dependency addition must pin and verify its actual version and target compatibility.

## Compatibility, Rollout, and Rollback

Only specifications and this task's artifacts may change. Preserve `go.mod`, product files, user-created `.gitignore`, model attributes, Trellis runtime scripts/settings, and existing unrelated untracked content.

Before writing specifications, snapshot the then-current files and record checksums. Review against that snapshot because the repository has no commits and ordinary `git diff HEAD` cannot describe this change. Recheck current contents so concurrent owner edits are not overwritten.

There is no runtime rollout or schema migration in this task. Roll back only this task's edits from its snapshot; do not use a repository-wide reset or clean. A commit requires a separate reviewed file list and owner confirmation.
