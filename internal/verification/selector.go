package verification

import (
	"encoding/json"
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
	frontendChecks := make(map[string]frontendCheckSpec)
	gpuiTouched, sqliteTouched, contractsTouched := false, false, false
	pythonFiles := make([]string, 0)
	pythonTests := make([]string, 0)
	artifacts := make([]string, 0)
	for _, touched := range input.Touched {
		path, err := normalizeTouchedPath(touched.Path)
		if err != nil {
			return session.VerificationPlanV1{}, err
		}
		if touched.Generated {
			artifacts = append(artifacts, path)
		}
		if strings.HasPrefix(path, "gpui/") {
			gpuiTouched = true
		}
		if strings.HasSuffix(path, ".py") && touched.Change != "deleted" {
			pythonFiles = append(pythonFiles, path)
			source, _ := os.ReadFile(filepath.Join(input.Workspace, filepath.FromSlash(path)))
			if strings.Contains(string(source), "import unittest") || strings.Contains(string(source), "from unittest import") {
				pythonTests = append(pythonTests, path)
			}
			continue
		}
		if strings.HasPrefix(path, "internal/store/sqlite/") && (strings.HasSuffix(path, ".sql") || strings.Contains(path, "/dbgen/")) {
			sqliteTouched = true
		}
		if path == "internal/app/contracts.go" || strings.HasPrefix(path, "internal/desktop/") || path == "gpui/crates/azem-ipc/src/generated.rs" {
			contractsTouched = true
		}
		if strings.HasSuffix(path, ".go") {
			goFiles = append(goFiles, path)
			goPackages["./"+filepath.ToSlash(filepath.Dir(path))] = struct{}{}
			continue
		}
		if manifest, root := nearestPackageManifest(input.Workspace, path); manifest != nil {
			if specs := frontendChecksForPath(root, path, manifest); len(specs) > 0 {
				for _, spec := range specs {
					frontendChecks[spec.id] = spec
				}
				continue
			}
		}
		if packagePath := nearestGoPackage(input.Workspace, path); packagePath != "" && !strings.HasPrefix(path, "gpui/") {
			goPackages[packagePath] = struct{}{}
		}
	}
	sort.Strings(goFiles)
	sort.Strings(artifacts)
	frontend := make([]frontendCheckSpec, 0, len(frontendChecks))
	for _, spec := range frontendChecks {
		frontend = append(frontend, spec)
	}
	sort.Slice(frontend, func(i, j int) bool { return frontend[i].id < frontend[j].id })
	checks := make([]session.VerificationCheckV1, 0, 8+len(artifacts)+len(input.Plan.Checks)+len(frontend))
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
	for _, spec := range frontend {
		appendCommand(spec.id, spec.cwd, 300_000, nil, spec.command...)
	}
	if sqliteTouched {
		appendCommand("sqlite-tests", "", 300_000, map[string]string{"GOWORK": "off"}, "go", "test", "./internal/store/sqlite")
	}
	if contractsTouched {
		appendCommand("contracts-check", "", 120_000, map[string]string{"GOWORK": "off"}, "go", "run", "./cmd/gen-contracts", "-check")
	}
	if gpuiTouched {
		appendCommand("gpui-tests", "gpui", 300_000, nil, "cargo", "test", "--workspace", "--all-targets")
	}
	if len(pythonFiles) > 0 {
		sort.Strings(pythonFiles)
		appendCommand("python-compile", "", 60_000, nil, append([]string{"python3", "-m", "py_compile"}, pythonFiles...)...)
	}
	if len(pythonTests) > 0 {
		sort.Strings(pythonTests)
		appendCommand("python-tests", "", 180_000, nil, append([]string{"python3", "-m", "unittest"}, pythonTests...)...)
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

type packageManifest struct {
	PackageManager string            `json:"packageManager"`
	Scripts        map[string]string `json:"scripts"`
}

type frontendCheckSpec struct {
	id      string
	cwd     string
	command []string
}

func nearestPackageManifest(workspace, path string) (*packageManifest, string) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil, ""
	}
	directory := filepath.Dir(filepath.Join(root, filepath.FromSlash(path)))
	for {
		manifestPath := filepath.Join(directory, "package.json")
		raw, readErr := os.ReadFile(manifestPath)
		if readErr == nil {
			var manifest packageManifest
			if json.Unmarshal(raw, &manifest) == nil {
				relative, relErr := filepath.Rel(root, directory)
				if relErr == nil && !strings.HasPrefix(relative, "..") {
					return &manifest, filepath.ToSlash(relative)
				}
			}
		}
		if directory == root || !strings.HasPrefix(directory, root+string(filepath.Separator)) {
			return nil, ""
		}
		directory = filepath.Dir(directory)
	}
}

func packageManagerCommand(packageManager string) string {
	packageManager = strings.TrimSpace(packageManager)
	if at := strings.IndexByte(packageManager, '@'); at >= 0 {
		packageManager = packageManager[:at]
	}
	switch packageManager {
	case "bun", "npm", "pnpm", "yarn":
		return packageManager
	default:
		return ""
	}
}

func isJavaScriptProjectFile(path string) bool {
	base := filepath.Base(filepath.FromSlash(path))
	switch base {
	case "package.json", "bun.lock", "package-lock.json", "pnpm-lock.yaml", "yarn.lock":
		return true
	}
	for _, prefix := range []string{"tsconfig", "vite.config", "vitest.config"} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".css", ".js", ".jsx", ".ts", ".tsx":
		return true
	default:
		return false
	}
}

func frontendChecksForPath(root, path string, manifest *packageManifest) []frontendCheckSpec {
	if manifest == nil || !isJavaScriptProjectFile(path) {
		return nil
	}
	manager := packageManagerCommand(manifest.PackageManager)
	if manager == "" {
		return nil
	}
	cwd := root
	idRoot := strings.ReplaceAll(root, "/", "-")
	if root == "." {
		cwd = ""
	}
	if idRoot == "" || idRoot == "." {
		idRoot = "frontend"
	}
	relative := path
	if root != "." {
		relative = strings.TrimPrefix(path, root+"/")
	}
	base := filepath.Base(filepath.FromSlash(relative))
	extension := strings.ToLower(filepath.Ext(base))
	isTest := strings.Contains(strings.ToLower(base), ".test.") || strings.Contains(strings.ToLower(base), ".spec.")
	add := func(script string) frontendCheckSpec {
		return frontendCheckSpec{id: idRoot + "-" + script, cwd: cwd, command: []string{manager, "run", script}}
	}
	scriptAvailable := func(script string) bool {
		return strings.TrimSpace(manifest.Scripts[script]) != ""
	}
	switch {
	case base == "package.json" || strings.HasPrefix(base, "tsconfig") || strings.HasPrefix(base, "vite.config") || strings.HasPrefix(base, "vitest.config"):
		specs := make([]frontendCheckSpec, 0, 3)
		for _, script := range []string{"typecheck", "test", "build"} {
			if scriptAvailable(script) {
				specs = append(specs, add(script))
			}
		}
		return specs
	case extension == ".css":
		if scriptAvailable("build") {
			return []frontendCheckSpec{add("build")}
		}
	case extension == ".ts" || extension == ".tsx" || extension == ".js" || extension == ".jsx":
		if isTest && scriptAvailable("test") {
			return []frontendCheckSpec{add("test")}
		}
		if scriptAvailable("typecheck") {
			return []frontendCheckSpec{add("typecheck")}
		}
		if scriptAvailable("build") {
			return []frontendCheckSpec{add("build")}
		}
	}
	return nil
}

func nearestGoPackage(workspace, path string) string {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return ""
	}
	directory := filepath.Dir(filepath.Join(root, filepath.FromSlash(path)))
	manifest, _ := nearestPackageManifest(workspace, path)
	if manifest != nil && isJavaScriptProjectFile(path) {
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
