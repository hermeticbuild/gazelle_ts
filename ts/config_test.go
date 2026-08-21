package ts

import (
	"reflect"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/rule"
)

func TestApplyDirective_Bools(t *testing.T) {
	cfg := newTsConfig()
	applyDirective(cfg, rule.Directive{Key: directiveEnabled, Value: "false"})
	if cfg.enabled {
		t.Fatalf("ts_enabled false: cfg.enabled = true")
	}
	applyDirective(cfg, rule.Directive{Key: directiveEnabled, Value: "true"})
	if !cfg.enabled {
		t.Fatalf("ts_enabled true: cfg.enabled = false")
	}
}

func TestApplyDirective_Strings(t *testing.T) {
	cfg := newTsConfig()
	applyDirective(cfg, rule.Directive{Key: directiveLibraryName, Value: "src"})
	applyDirective(cfg, rule.Directive{Key: directiveTestName, Value: "spec"})
	applyDirective(cfg, rule.Directive{Key: directiveVisualLibraryName, Value: "visuals"})
	applyDirective(cfg, rule.Directive{Key: directiveNpmLinkPattern, Value: "//pnpm:node_modules/{pkg}"})

	if cfg.libraryName != "src" {
		t.Errorf("libraryName = %q", cfg.libraryName)
	}
	if cfg.testName != "spec" {
		t.Errorf("testName = %q", cfg.testName)
	}
	if cfg.visualLibraryName != "visuals" {
		t.Errorf("visualLibraryName = %q", cfg.visualLibraryName)
	}
	if cfg.npmLinkPattern != "//pnpm:node_modules/{pkg}" {
		t.Errorf("npmLinkPattern = %q", cfg.npmLinkPattern)
	}
}

func TestApplyDirective_Visibility(t *testing.T) {
	cfg := newTsConfig()
	applyDirective(cfg, rule.Directive{Key: directiveVisibility, Value: "//foo:__pkg__ //bar:__pkg__"})
	want := []string{"//foo:__pkg__", "//bar:__pkg__"}
	if !reflect.DeepEqual(cfg.visibility, want) {
		t.Errorf("visibility = %v want %v", cfg.visibility, want)
	}
}

func TestApplyDirective_AppendDirectives(t *testing.T) {
	cfg := newTsConfig()
	// Test patterns and extensions append rather than replace.
	applyDirective(cfg, rule.Directive{Key: directiveTestPattern, Value: "*.spec.ts"})
	applyDirective(cfg, rule.Directive{Key: directiveVisualLibraryPattern, Value: "*.stories.tsx"})
	applyDirective(cfg, rule.Directive{Key: directiveExtension, Value: ".mts"})
	applyDirective(cfg, rule.Directive{Key: directiveTestData, Value: "//:fixtures"})
	applyDirective(cfg, rule.Directive{Key: directiveTsconfigTypes, Value: "node react"})
	applyDirective(cfg, rule.Directive{Key: directiveResolveGlobal, Value: "process //:node_modules/@types/node"})

	if !contains(cfg.testPatterns, "*.spec.ts") {
		t.Errorf("testPatterns missing *.spec.ts: %v", cfg.testPatterns)
	}
	if !contains(cfg.visualLibraryPatterns, "*.stories.tsx") {
		t.Errorf("visualLibraryPatterns missing *.stories.tsx: %v", cfg.visualLibraryPatterns)
	}
	if !contains(cfg.extensions, ".mts") {
		t.Errorf("extensions missing .mts: %v", cfg.extensions)
	}
	if !contains(cfg.testData, "//:fixtures") {
		t.Errorf("testData missing //:fixtures: %v", cfg.testData)
	}
	if !reflect.DeepEqual(cfg.tsconfigTypes, []string{"node", "react"}) {
		t.Errorf("tsconfigTypes = %v, want [node react]", cfg.tsconfigTypes)
	}
	if got, want := cfg.globalResolves["process"], "//:node_modules/@types/node"; got != want {
		t.Errorf("globalResolves[process] = %q, want %q", got, want)
	}

	// Re-applying the same value should not duplicate.
	applyDirective(cfg, rule.Directive{Key: directiveTestPattern, Value: "*.spec.ts"})
	applyDirective(cfg, rule.Directive{Key: directiveVisualLibraryPattern, Value: "*.stories.tsx"})
	applyDirective(cfg, rule.Directive{Key: directiveTsconfigTypes, Value: "node"})
	count := 0
	for _, p := range cfg.testPatterns {
		if p == "*.spec.ts" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 occurrence of *.spec.ts, got %d", count)
	}
	count = 0
	for _, p := range cfg.visualLibraryPatterns {
		if p == "*.stories.tsx" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 occurrence of *.stories.tsx, got %d", count)
	}
	count = 0
	for _, typ := range cfg.tsconfigTypes {
		if typ == "node" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 occurrence of node, got %d", count)
	}
}

func TestClone_Independent(t *testing.T) {
	parent := newTsConfig()
	parent.libraryName = "lib"
	parent.testPatterns = append(parent.testPatterns, "*.spec.ts")
	parent.visualLibraryPatterns = append(parent.visualLibraryPatterns, "*.stories.tsx")
	parent.tsconfigTypes = append(parent.tsconfigTypes, "node")
	parent.globalResolves["process"] = "//:node_modules/@types/node"

	child := parent.clone()
	child.libraryName = "src"
	child.testPatterns = append(child.testPatterns, "**/__tests__/**")
	child.visualLibraryPatterns = append(child.visualLibraryPatterns, "**/*.storybook.tsx")
	child.tsconfigTypes = append(child.tsconfigTypes, "react")
	child.globalResolves["chrome"] = "//:node_modules/@types/chrome"

	if parent.libraryName != "lib" {
		t.Errorf("parent libraryName mutated: %q", parent.libraryName)
	}
	if contains(parent.testPatterns, "**/__tests__/**") {
		t.Errorf("parent testPatterns mutated: %v", parent.testPatterns)
	}
	if contains(parent.visualLibraryPatterns, "**/*.storybook.tsx") {
		t.Errorf("parent visualLibraryPatterns mutated: %v", parent.visualLibraryPatterns)
	}
	if contains(parent.tsconfigTypes, "react") {
		t.Errorf("parent tsconfigTypes mutated: %v", parent.tsconfigTypes)
	}
	if _, ok := parent.globalResolves["chrome"]; ok {
		t.Errorf("parent globalResolves mutated: %v", parent.globalResolves)
	}
}

func TestApplyDirective_BundlerConfigPattern(t *testing.T) {
	cfg := newTsConfig()
	applyDirective(cfg, rule.Directive{
		Key:   directiveBundlerConfigPattern,
		Value: "vite.config.* vite_config",
	})
	applyDirective(cfg, rule.Directive{
		Key:   directiveBundlerConfigPattern,
		Value: "vitest.config.* vitest_config",
	})

	want := []bundlerConfigSpec{
		{Pattern: "vite.config.*", Name: "vite_config"},
		{Pattern: "vitest.config.*", Name: "vitest_config"},
	}
	if !reflect.DeepEqual(cfg.bundlerConfigSpecs, want) {
		t.Errorf("bundlerConfigSpecs = %v, want %v", cfg.bundlerConfigSpecs, want)
	}

	// Re-applying the same value should not duplicate.
	applyDirective(cfg, rule.Directive{
		Key:   directiveBundlerConfigPattern,
		Value: "vite.config.* vite_config",
	})
	if len(cfg.bundlerConfigSpecs) != 2 {
		t.Errorf("duplicate spec accepted: %v", cfg.bundlerConfigSpecs)
	}

	// Malformed (single field, missing name) silently ignored.
	applyDirective(cfg, rule.Directive{
		Key:   directiveBundlerConfigPattern,
		Value: "vite.config.*",
	})
	if len(cfg.bundlerConfigSpecs) != 2 {
		t.Errorf("malformed spec accepted: %v", cfg.bundlerConfigSpecs)
	}
}

func TestClone_BundlerConfigSpecs(t *testing.T) {
	parent := newTsConfig()
	parent.bundlerConfigSpecs = []bundlerConfigSpec{
		{Pattern: "vite.config.*", Name: "vite_config"},
	}
	child := parent.clone()
	child.bundlerConfigSpecs = append(child.bundlerConfigSpecs, bundlerConfigSpec{
		Pattern: "tailwind.config.ts", Name: "tailwind_config",
	})
	if len(parent.bundlerConfigSpecs) != 1 {
		t.Errorf("parent mutated by child: %v", parent.bundlerConfigSpecs)
	}
}

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
