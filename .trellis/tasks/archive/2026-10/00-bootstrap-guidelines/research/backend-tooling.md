# Backend Tooling Research

## Scope

The owner selected Gin, GORM with SQLite, Fx dependency injection/lifecycle management, and Swagger API documentation. The Wire section below records the superseded initial selection and maintenance evidence, not current implementation guidance. Current Fx rationale and requirements are in `di-comparison.md`. This note does not add dependencies or prove compatibility with Zhulong's Go 1.27.1 toolchain.

## Historical Wire Maintenance and Execution Model

Source: [google/wire README](https://github.com/google/wire/blob/main/README.md), fetched from `https://raw.githubusercontent.com/google/wire/main/README.md`.

Exact upstream warning:

> This project is no longer maintained.
>
> If you wish to update or extend wire, please do so in a fork.

The same README describes Wire as a code-generation tool connecting dependencies through function parameters and operating without runtime state or reflection.

Historical implications, superseded by the owner's explicit Fx selection:

- Wire was considered as a build/development-time generator with ordinary generated Go initialization code and no deployed DI runtime service.
- The unmaintained generator would have required a pinned version and a Go 1.27.1 generation compatibility check; neither was performed.
- Do not introduce Wire, a Wire fork, or DI-generated source files into the current Fx plan. Retain the maintenance warning as selection history only.

## Swagger Generation Scope

Sources fetched:

- [swaggo/swag README](https://github.com/swaggo/swag/blob/master/README.md), raw URL `https://raw.githubusercontent.com/swaggo/swag/master/README.md`.
- [swaggo/gin-swagger README](https://github.com/swaggo/gin-swagger/blob/master/README.md), raw URL `https://raw.githubusercontent.com/swaggo/gin-swagger/master/README.md`.

The inspected Swag README's Gin walkthrough states:

> After using `swag init` to generate Swagger 2.0 docs, import the following packages:

The inspected gin-swagger README describes itself as:

> gin middleware to automatically generate RESTful API documentation with Swagger 2.0.

Implications:

- `swaggo/swag` plus `gin-swagger` is a documented candidate for a Gin/Swagger 2.0 workflow.
- Do not describe these inspected instructions as proof of OpenAPI 3.x generation. Confirm the required schema generation and pin compatible tooling before implementing it.
- These are mutable branch README observations, not a tested or locked release matrix. Swagger UI rendering and specification generation are separate responsibilities.
- Do not copy upstream `@latest` install instructions into a reproducibility requirement without first choosing explicit versions.

## Verification Limits

Only the upstream README content and existing repository/toolchain evidence were inspected. No packages were installed, no native engine was built, no Wire code was generated, and no Swagger document was produced. API schema/exposure/i18n checks must not be represented as passing application tests.
