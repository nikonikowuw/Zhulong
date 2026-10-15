# Technical Design: Unify Entity Primary Key Generation with ULID

## 1. Architectural Overview & Context

In Zhulong (an edge appliance host running on SQLite in WAL mode), entity surrogate keys must balance:
1. **B-Tree Locality**: Sequential/monotonic writes minimize SQLite page splits and disk I/O amplification.
2. **Offline & Edge Autonomy**: Zero coordination / centralized ID authority needed.
3. **Clean Decoupling**: Primary keys are purely opaque surrogate identifiers. Human meaning belongs to display names, and relationships belong to foreign keys and unique constraints (`UNIQUE(camera_id, algorithm_id)`).
4. **URL & Filesystem Safety**: Compact representation without symbols or path-breaking characters (`/`, `?`, `#`, `\`).

ULID (Crockford's Base32, 26 characters) precisely satisfies all four dimensions:
- 48-bit UNIX millisecond timestamp (sorted lexicographically)
- 80-bit cryptographically secure randomness
- Monotonicity support within the same millisecond

```
+------------------+------------------+
| 48-bit Timestamp | 80-bit Randomness|
+------------------+------------------+
|<----------------- 128 bits -------->|
|<--------- 26 chars Base32 ---------->|
```

## 2. Package Boundaries & Module Design

### 2.1 New Shared Utility: `internal/idutil`

```go
package idutil

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Monotonic entropy source guarded by sync.Mutex for thread-safety.
var (
	entropyMu sync.Mutex
	entropy   = ulid.Monotonic(rand.Reader, 0)
)

// New generates a cryptographically secure, monotonic 26-character Crockford Base32 ULID.
func New() string {
	entropyMu.Lock()
	defer entropyMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}

// IsValid checks whether a string conforms to Crockford Base32 26-char ULID specification.
func IsValid(s string) bool

// Time extracts the embedded UTC timestamp from a ULID.
func Time(s string) (time.Time, error)
```

### 2.2 Domain Changes

1. **`internal/person`**:
   - `service.go`: Replace `newID()` body to return `idutil.New()`.
   - `Person.ID` and `Face.ID` inherit ULID as internal surrogate keys.
   - `Person.PersonID` remains the external business key (optional/nullable `*string`), completely decoupled from `Person.ID` and never populated by ULID fallback.
   - Preserves `{owner_row_id}_{face_id}.ext` file naming convention seamlessly (26 chars Crockford Base32).

2. **`internal/camera`**:
   - `service.go`:
     - Delete `s.createMu sync.Mutex` in `Service` struct (used only for `GetNextNumericID`).
     - In `CreateCamera`: if `camID == ""`, simply assign `camID = idutil.New()`.
   - `store.go`:
     - Remove `GetNextNumericID(ctx context.Context) (string, error)` from `Store` interface.
     - Remove `(*gormCameraStore).GetNextNumericID` implementation.
   - `store_test.go`:
     - Update / clean up test cases targeting `GetNextNumericID`.

3. **`internal/algorithm`**:
   - `handler.go`:
     - In `CreateTask`: if `task.ID == ""` (or omitted), assign `task.ID = idutil.New()` instead of `"task_" + camID + "_" + algoID`.
   - `handler_test.go` & `audit_test.go`:
     - Update tests that assumed static task ID naming: read created task ID from response JSON `data.id` or supply an explicit `id` in the test request payload.

## 3. Storage & Schema Considerations

- **DDL Migration**: None. Tables `persons`, `person_faces`, `cameras`, and `analysis_tasks` already define `id TEXT PRIMARY KEY NOT NULL`.
- **Backward Compatibility**:
  - Existing cameras with integer strings (`"1"`, `"2"`) or custom strings (`"cam-01"`) continue working without interruption.
  - Client-supplied `id` in `CreateCameraRequest` and `CreateTaskRequest` remains supported.

## 4. Concurrency & Performance

- **Elimination of Lock Contention**: Removing `s.createMu` in `camera.Service` eliminates a cross-request serialization choke point.
- **SQLite B-Tree Write Pattern**: ULIDs generated sequentially will monotonically increase in their 48-bit timestamp prefix, appending to SQLite B-Tree leaf pages without triggering mid-page splits.
- **Thread Safety**: `idutil.New` uses a lightweight `sync.Mutex` around `ulid.Monotonic`, benchmarked at <150ns per generation.

## 5. Rollback & Failure Recovery

If rolled back:
- No database DDL reversal required.
- Any generated ULID remains a valid `TEXT` primary key.
