package verification

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Viking602/azem/internal/session"
)

type TouchedFileV1 struct {
	Path      string
	Change    string
	Generated bool
}

type SelectInput struct {
	Workspace string
	Work      session.WorkSpecV1
	Plan      session.VerificationPlanV1
	Touched   []TouchedFileV1
}

// SelectDeterministicChecks orders compiler, test, and generated-artifact
// checks before semantic criterion checks. Commands are argv arrays with an
// explicit working directory and environment; no shell interpolation occurs.
func SelectDeterministicChecks(input SelectInput) (session.VerificationPlanV1, error) {
	if input.Work.ID == "" || input.Plan.WorkSpecID != input.Work.ID || strings.TrimSpace(input.Workspace) == "" {
		return session.VerificationPlanV1{}, fmt.Errorf("verification: selector work and plan do not match")
	}
	criterionIDs := make([]string, 0, len(input.Work.Criteria))
	for _, criterion := range input.Work.Criteria {
		criterionIDs = append(criterionIDs, criterion.ID)
	}
	if len(criterionIDs) == 0 {
		return session.VerificationPlanV1{}, fmt.Errorf("verification: selector has no criteria")
	}
	goFiles := make([]string, 0)
	goPackages := make(map[string]struct{})
	frontendTests := make([]string, 0)
	frontendTouched, pythonTouched, sqliteTouched, contractsTouched := false, false, false, false
	artifacts := make([]string, 0)
	for _, touched := range input.Touched {
		path, err := normalizeTouchedPath(touched.Path)
		if err != nil {
			return session.VerificationPlanV1{}, err
		}
		if touched.Generated {
			artifacts = append(artifacts, path)
		}
		if strings.HasPrefix(path, "frontend/") {
			frontendTouched = true
			if strings.HasSuffix(path, ".test.ts") || strings.HasSuffix(path, ".test.tsx") {
				frontendTests = append(frontendTests, strings.TrimPrefix(path, "frontend/"))
			}
		}
		if strings.HasSuffix(path, ".py") {
			pythonTouched = true
		}
		if strings.HasPrefix(path, "internal/store/sqlite/") && (strings.HasSuffix(path, ".sql") || strings.Contains(path, "/dbgen/")) {
			sqliteTouched = true
		}
		if path == "internal/app/contracts.go" || strings.HasPrefix(path, "internal/desktop/") || path == "frontend/src/contracts.ts" {
			contractsTouched = true
		}
		if strings.HasSuffix(path, ".go") {
			goFiles = append(goFiles, path)
			goPackages["./"+filepath.ToSlash(filepath.Dir(path))] = struct{}{}
			continue
		}
		if packagePath := nearestGoPackage(input.Workspace, path); packagePath != "" && !strings.HasPrefix(path, "frontend/") {
			goPackages[packagePath] = struct{}{}
		}
	}
	sort.Strings(goFiles)
	sort.Strings(frontendTests)
	sort.Strings(artifacts)
	checks := make([]session.VerificationCheckV1, 0, 8+len(artifacts)+len(input.Plan.Checks))
	appendCommand := func(id, cwd string, timeout int64, environment map[string]string, command ...string) {
		checks = append(checks, session.VerificationCheckV1{
			ID: id, CriterionIDs: append([]string(nil), criterionIDs...), Kind: "command",
			Command: append([]string(nil), command...), CWD: cwd, Environment: environment, TimeoutMS: timeout,
		})
	}
	if len(goFiles) > 0 {
		appendCommand("gofmt", "", 60_000, nil, append([]string{"gofmt", "-d"}, goFiles...)...)
	}
	if len(goPackages) > 0 {
		packages := make([]string, 0, len(goPackages))
		for packagePath := range goPackages {
			packages = append(packages, packagePath)
		}
		sort.Strings(packages)
		appendCommand("go-test", "", 300_000, map[string]string{"GOWORK": "off"}, append([]string{"go", "test"}, packages...)...)
	}
	if sqliteTouched {
		appendCommand("sqlite-tests", "", 300_000, map[string]string{"GOWORK": "off"}, "go", "test", "./internal/store/sqlite")
	}
	if contractsTouched {
		appendCommand("contracts-check", "", 120_000, map[string]string{"GOWORK": "off"}, "go", "run", "./cmd/gen-contracts", "-check")
	}
	if frontendTouched {
		appendCommand("frontend-typecheck", "frontend", 180_000, nil, "bun", "run", "typecheck")
		if len(frontendTests) > 0 {
			appendCommand("frontend-tests", "frontend", 300_000, nil, append([]string{"bun", "run", "test", "--"}, frontendTests...)...)
		}
	}
	if pythonTouched {
		appendCommand("python-tests", "", 180_000, nil, "python3", "-m", "unittest", "discover", "eval/harbor", "-p", "*_test.py")
	}
	for _, artifact := range artifacts {
		checks = append(checks, session.VerificationCheckV1{
			ID: "artifact:" + artifact, CriterionIDs: append([]string(nil), criterionIDs...), Kind: "artifact", ArtifactRef: artifact,
		})
	}
	for _, check := range input.Plan.Checks {
		if check.Kind == "criterion" {
			checks = append(checks, check)
		}
	}
	selected := input.Plan
	selected.Checks = checks
	if err := selected.Validate(); err != nil {
		return session.VerificationPlanV1{}, err
	}
	return selected, nil
}

func normalizeTouchedPath(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("verification: unsafe touched path %q", path)
	}
	return filepath.ToSlash(path), nil
}

func nearestGoPackage(workspace, path string) string {
	directory := filepath.Dir(filepath.Join(workspace, filepath.FromSlash(path)))
	root, err := filepath.Abs(workspace)
	if err != nil {
		return ""
	}
	for {
		entries, readErr := os.ReadDir(directory)
		if readErr == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
					relative, relErr := filepath.Rel(root, directory)
					if relErr == nil && relative != "." && !strings.HasPrefix(relative, "..") {
						return "./" + filepath.ToSlash(relative)
					}
				}
			}
		}
		if directory == root || !strings.HasPrefix(directory, root+string(filepath.Separator)) {
			return ""
		}
		directory = filepath.Dir(directory)
	}
}
