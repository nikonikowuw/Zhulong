package audit

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 审计动作的显示名只存在于前端 locales，Go 侧没有任何编译期约束：
// 一旦新增 Action* 常量却忘了补文案，审计列表会静默回退成裸 key（如 task.update）。
// 这个测试把「后端写什么」和「前端怎么显示」钉在一起：
//
//	internal/**/model.go 的 Action* 常量  ⊆  web/src/features/audit/locales/*.json 的 actionNames
//
// 前端侧的反向约束（清单 ↔ locale 双向一致、多语对齐）由
// web/src/features/audit/data/audit-actions.test.ts 负责。
func TestAuditActionLocalesCoverBackendActions(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	backendActions := collectBackendAuditActions(t, repoRoot)
	if len(backendActions) == 0 {
		t.Fatal("no backend audit action constants found; the scanner regex no longer matches model.go")
	}

	locales := []string{"zh-Hans.json", "zh-Hant.json", "en.json"}
	for _, locale := range locales {
		t.Run(locale, func(t *testing.T) {
			actionNames := loadActionNames(t, repoRoot, locale)

			var missing []string
			for _, action := range backendActions {
				if _, ok := actionNames[action]; !ok {
					missing = append(missing, action)
				}
			}
			if len(missing) > 0 {
				t.Fatalf("%s is missing actionNames entries for backend actions %v; "+
					"the audit list would render the raw key instead of a label",
					locale, missing)
			}
		})
	}
}

// auditActionPattern matches the `ActionSomething = "domain.verb"` constant style used by
// every domain model.go. Only dotted lowercase values are taken: those are the audit
// action strings persisted in the audit log.
var auditActionPattern = regexp.MustCompile(`(?m)^\s*Action[A-Za-z0-9]+\s*=\s*"([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+)"`)

func collectBackendAuditActions(t *testing.T, repoRoot string) []string {
	t.Helper()

	internalDir := filepath.Join(repoRoot, "internal")
	seen := make(map[string]struct{})

	err := filepath.WalkDir(internalDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range auditActionPattern.FindAllStringSubmatch(string(content), -1) {
			seen[match[1]] = struct{}{}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", internalDir, err)
	}

	actions := make([]string, 0, len(seen))
	for action := range seen {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return actions
}

func loadActionNames(t *testing.T, repoRoot, locale string) map[string]string {
	t.Helper()

	path := filepath.Join(repoRoot, "web", "src", "features", "audit", "locales", locale)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var payload struct {
		ActionNames map[string]string `json:"actionNames"`
	}
	if err = json.Unmarshal(content, &payload); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(payload.ActionNames) == 0 {
		t.Fatalf("%s has no actionNames entries", path)
	}
	return payload.ActionNames
}
