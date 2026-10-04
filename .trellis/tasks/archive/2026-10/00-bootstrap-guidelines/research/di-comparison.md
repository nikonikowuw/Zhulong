# Wire versus Fx for Zhulong

## Decision Status

The owner explicitly approved replacing Wire with Fx. Fx is now the selected dependency-injection and lifecycle framework; Wire content below is retained only as historical comparison evidence. No dependencies were installed and no DI benchmark or compatibility test was run.

## Sources Inspected

- [Wire README](https://github.com/google/wire/blob/main/README.md): warns that the project is no longer maintained; describes generated constructor wiring without runtime reflection/state.
- [Fx application lifecycle](https://uber-go.github.io/fx/lifecycle.html): constructors are registered during initialization and called as needed by invoked functions; startup hooks execute in append order, shutdown hooks in reverse append order. Hooks must not synchronously run permanent workloads.
- [Fx lifecycle source](https://github.com/uber-go/fx/blob/master/lifecycle.go): `Lifecycle.Append(Hook)` with `OnStart`/`OnStop` callbacks accepting `context.Context` and returning `error`; stop callbacks are not executed for startup hooks skipped because of an earlier failure.
- [Fx README](https://github.com/uber-go/fx/blob/master/README.md): upstream entry point, not evidence of a pinned release or Go 1.27.1 compatibility.

Upstream branch files are mutable. Recheck the actual pinned versions at implementation time.

## Comparison

| Concern | Wire | Fx |
| --- | --- | --- |
| Wiring | Generated ordinary Go; dependency graph checked during generation | Runtime dependency graph via reflection; graph errors must be covered by validation/startup tests |
| Runtime cost | No DI container required in the deployed program | Graph construction/reflection mainly at initialization; do not place container resolution in media/request hot paths |
| Lifecycle | Constructors and cleanup can be generated, but service orchestration is application-owned | Built-in start/stop hooks help coordinate long-lived services |
| Maintenance evidence | Official upstream explicitly says unmaintained | Dedicated lifecycle framework; evaluate pinned release/toolchain rather than assuming compatibility |
| Debugging | Generated call chain is easy to inspect | Less generated-code maintenance, but registration and lifecycle order require care |
| Packaging | Generator is a development/build-time tool | Fx is linked into the Go program; it requires no separate production daemon |

## Adopted Rationale

Prefer Fx for a new Zhulong service because SQLite, Gin, native engine handles, and long-running Pipeline workers require coordinated lifecycle management, and there is no existing Wire implementation to migrate. Wire remains reasonable if the owner prioritizes generated explicit calls and no runtime DI machinery, and accepts generator maintenance responsibility plus application-owned lifecycle orchestration.

This is an engineering fit judgment, not measured performance evidence. Both approaches can implement safe shutdown; neither automatically understands native resource ownership or database migration policy.

## Guardrails for the Selected Fx Design

- Retain ordinary constructors and methods in business packages. Confine Fx registration/lifecycle adapters to application composition where possible; do not inject a container/service locator into handlers.
- Providers are demand-driven. Merely listing a provider does not guarantee its component is constructed or its lifecycle hook runs; the application root must explicitly consume required services.
- Express migration readiness as an explicit startup dependency/gate before API acceptance or camera/Pipeline workers. Do not rely on the order of `fx.Provide` arguments.
- Hook order is append/reverse-append order. Register hooks through deliberate dependencies or use one application coordinator for multi-phase shutdown. Closing HTTP, stopping/draining workers, releasing the engine, and closing SQLite must follow actual resource users, not an assumed magic order.
- Keep constructors free of started goroutines and unrecoverable native side effects. A failing initialization/start hook must clean up its own partial allocation; other hooks cannot be assumed to reclaim resources they do not own.
- Use an application-owned lifetime context for persistent workers, not the startup-hook context. Hooks should return after bounded startup/readiness work and stop/join their workers on shutdown.
- A hook deadline does not forcibly cancel a blocking CGO call or make it safe to free native memory. C++ must implement its own stop/unblock/join protocol; never destroy an engine still executing.
- Keep slow database migrations explicit: define startup timeout policy and failure behavior rather than dispatching migration in the background to satisfy a hook deadline.
- Validate graph construction and exercise actual startup, failed-start rollback, migration failure, repeated stop, and shutdown with synthetic native backends. Recheck the project toolchain (`go.mod`: 1.27.1) against the selected Fx release.
