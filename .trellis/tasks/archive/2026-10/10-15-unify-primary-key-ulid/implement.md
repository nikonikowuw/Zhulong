# Implementation Plan: Unify Entity Primary Key Generation with ULID

## 1. Prerequisites & Dependencies

- [x] Add `github.com/oklog/ulid/v2` to `go.mod` via `go get github.com/oklog/ulid/v2`.
- [x] Run `go mod tidy` and verify `go.mod` / `go.sum`.

## 2. Phase 1: Shared ID Utility (`internal/idutil`)

- [x] Create `internal/idutil/idutil.go`:
  - `New() string`: thread-safe monotonic Crockford Base32 ULID.
  - `IsValid(s string) bool`: validate 26-char Crockford Base32 string.
  - `Time(s string) (time.Time, error)`: extract UTC timestamp.
- [x] Create `internal/idutil/idutil_test.go`:
  - Monotonicity test across rapid iterations.
  - Concurrent generation race test (`-race`).
  - Validation test for valid / invalid ULIDs.
  - Round-trip timestamp extraction test.
- [x] Validation command:
  ```bash
  go test -race -v ./internal/idutil/...
  ```

## 3. Phase 2: Refactor Personnel & Face Domain (`internal/person`)

- [x] In `internal/person/service.go`:
  - Update `newID()` to call `idutil.New()`.
  - Remove redundant `crypto/rand` import if not used elsewhere in `service.go`.
- [x] In `internal/person/person_test.go` / `service_test.go` / `handler_test.go`:
  - Verify that newly created persons and faces have valid 26-character ULIDs.
- [x] Validation command:
  ```bash
  python3 native/scripts/build.py go test -race -v ./internal/person/...
  ```

## 4. Phase 3: Refactor Camera Domain (`internal/camera`)

- [x] In `internal/camera/store.go`:
  - Remove `GetNextNumericID(ctx context.Context) (string, error)` from `Store` interface.
  - Remove `gormCameraStore.GetNextNumericID` implementation.
- [x] In `internal/camera/service.go`:
  - Remove `s.createMu sync.Mutex` from `Service` struct.
  - Update `CreateCamera`: when `camID == ""`, generate `camID = idutil.New()`.
- [x] In `internal/camera/store_test.go`:
  - Remove or update tests that assert on `GetNextNumericID`.
- [x] In `internal/camera/service_test.go` & other camera tests:
  - Verify camera creation generates valid ULIDs when ID is omitted.
  - Verify explicit custom ID still works.
- [x] Validation command:
  ```bash
  python3 native/scripts/build.py go test -race -v ./internal/camera/...
  ```

## 5. Phase 4: Refactor Analysis Task Domain (`internal/algorithm`)

- [x] In `internal/algorithm/handler.go`:
  - In `CreateTask`: replace `task.ID = "task_" + task.CameraID + "_" + task.AlgorithmID` with `task.ID = idutil.New()`.
- [x] In `internal/algorithm/handler_test.go`:
  - When testing `DeleteTask` on a dynamically created task, parse the generated ID from the `CreateTask` response payload.
- [x] In `internal/algorithm/audit_test.go`:
  - Update audit task test to either parse the generated task ID or provide an explicit test task ID in the request payload.
- [x] Validation command:
  ```bash
  python3 native/scripts/build.py go test -race -v ./internal/algorithm/...
  ```

## 6. Phase 5: Update Specifications & Documentation

- [x] Update `.trellis/spec/backend/database-guidelines.md`:
  - Add section documenting ULID (`idutil.New()`) as the mandated surrogate primary key for `TEXT PRIMARY KEY` entities (`persons`, `person_faces`, `cameras`, `analysis_tasks`).
  - Document that custom/external business keys (like `persons.person_id`) remain distinct from internal surrogate ULID keys.

## 7. Phase 6: Full Verification Gate

- [x] Run full project checks:
  ```bash
  make check
  ```
  Ensures frontend lint/typecheck/test, native tests, api-docs, and backend checks pass cleanly.
