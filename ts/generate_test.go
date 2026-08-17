package ts

import (
	"reflect"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

func TestIsTypeScriptFile(t *testing.T) {
	cfg := newTsConfig()
	cases := map[string]bool{
		"a.ts":      true,
		"a.tsx":     true,
		"a.js":      false,
		"a.json":    false,
		"a.d.ts":    true,
		"a.test.ts": true,
	}
	for name, want := range cases {
		if got := isTypeScriptFile(name, cfg); got != want {
			t.Errorf("isTypeScriptFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestIsTypeScriptFile_CustomExtensions(t *testing.T) {
	cfg := newTsConfig()
	cfg.extensions = append(cfg.extensions, ".mts")
	if !isTypeScriptFile("foo.mts", cfg) {
		t.Errorf("expected .mts to be recognized after directive")
	}
}

func TestIsTestFile_DefaultPatterns(t *testing.T) {
	cfg := newTsConfig()
	cases := map[string]bool{
		"foo.test.ts":                true,
		"foo.test.tsx":               true,
		"foo.spec.ts":                true,
		"nested/foo.test.ts":         true,
		"nested/foo.spec.tsx":        true,
		"tests/index.ts":             true,
		"test/main.ts":               true,
		"src/foo.ts":                 false,
		"deeply/nested/test/file.ts": false, // patterns are top-level prefixes
	}
	for name, want := range cases {
		if got := isTestFile(name, cfg); got != want {
			t.Errorf("isTestFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestIsTestFile_CustomPatterns(t *testing.T) {
	cfg := newTsConfig()
	cfg.testPatterns = append(cfg.testPatterns, "*.spec.ts")
	if !isTestFile("foo.spec.ts", cfg) {
		t.Errorf("custom *.spec.ts pattern not picked up")
	}
}

func TestIsVisualLibraryFile_DefaultPatterns(t *testing.T) {
	cfg := newTsConfig()
	cases := map[string]bool{
		"Button.story.tsx":           true,
		"Button.visual.tsx":          true,
		"nested/Button.story.tsx":    true,
		"nested/Button.visual.tsx":   true,
		"Button.stories.tsx":         false,
		"Button.story.ts":            false,
		"Button.visual.ts":           false,
		"Button.test.tsx":            false,
		"src/Button.tsx":             false,
		"deeply/nested/Button.story": false,
	}
	for name, want := range cases {
		if got := isVisualLibraryFile(name, cfg); got != want {
			t.Errorf("isVisualLibraryFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestIsVisualLibraryFile_CustomPatterns(t *testing.T) {
	cfg := newTsConfig()
	cfg.visualLibraryPatterns = append(cfg.visualLibraryPatterns, "*.stories.tsx")
	if !isVisualLibraryFile("Button.stories.tsx", cfg) {
		t.Errorf("custom *.stories.tsx pattern not picked up")
	}
}

func TestMatchTestPattern(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*.test.ts", "foo.test.ts", true},
		{"*.test.ts", "foo.ts", false},
		{"tests/**", "tests/foo.ts", true},
		{"tests/**", "tests/sub/foo.ts", true},
		{"tests/**", "src/tests/foo.ts", false},
		{"**/*.spec.ts", "foo.spec.ts", true},
		{"**/*.spec.ts", "nested/foo.spec.ts", true},
		{"foo.ts", "foo.ts", true},
		{"foo.ts", "bar.ts", false},
	}
	for _, c := range cases {
		got := matchTestPattern(c.pattern, c.name)
		if got != c.want {
			t.Errorf("matchTestPattern(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestResolveRuleNames(t *testing.T) {
	cases := []struct {
		name              string
		cfg               *tsConfig
		rel               string
		wantLib           string
		wantTest          string
		wantVisualLibrary string
	}{
		{
			name:              "default uses package basename",
			cfg:               newTsConfig(),
			rel:               "apps/web",
			wantLib:           "web",
			wantTest:          "web_test",
			wantVisualLibrary: "web_visual_library",
		},
		{
			name:              "deeply nested uses leaf basename",
			cfg:               newTsConfig(),
			rel:               "packages/utils/math/deep",
			wantLib:           "deep",
			wantTest:          "deep_test",
			wantVisualLibrary: "deep_visual_library",
		},
		{
			name:              "repo root falls back to literal names",
			cfg:               newTsConfig(),
			rel:               "",
			wantLib:           "lib",
			wantTest:          "test",
			wantVisualLibrary: "visual_library",
		},
		{
			name: "directive overrides win",
			cfg: func() *tsConfig {
				c := newTsConfig()
				c.libraryName = "src"
				c.testName = "spec"
				c.visualLibraryName = "visuals"
				return c
			}(),
			rel:               "packages/foo",
			wantLib:           "src",
			wantTest:          "spec",
			wantVisualLibrary: "visuals",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lib, test, visualLibrary := resolveRuleNames(c.cfg, c.rel)
			if lib != c.wantLib {
				t.Errorf("lib = %q, want %q", lib, c.wantLib)
			}
			if test != c.wantTest {
				t.Errorf("test = %q, want %q", test, c.wantTest)
			}
			if visualLibrary != c.wantVisualLibrary {
				t.Errorf("visual library = %q, want %q", visualLibrary, c.wantVisualLibrary)
			}
		})
	}
}

func TestCollectSrcs(t *testing.T) {
	cfg := newTsConfig()
	files := []string{
		"main.ts",
		"helper.ts",
		"types.tsx",
		"main.test.ts",
		"Button.story.tsx",
		"Card.visual.tsx",
		"nested/Card.story.tsx",
		"nested/Dialog.visual.tsx",
		"nested/main.spec.ts",
		"tests/integration.ts",
		"README.md",
		"package.json",
	}
	parts := collectSrcs(files, cfg)

	wantLibs := []string{"helper.ts", "main.ts", "types.tsx"}
	wantTests := []string{"main.test.ts", "nested/main.spec.ts", "tests/integration.ts"}
	wantVisualLibraries := []string{"Button.story.tsx", "Card.visual.tsx", "nested/Card.story.tsx", "nested/Dialog.visual.tsx"}
	if !reflect.DeepEqual(parts.lib, wantLibs) {
		t.Errorf("libs = %v, want %v", parts.lib, wantLibs)
	}
	if !reflect.DeepEqual(parts.test, wantTests) {
		t.Errorf("tests = %v, want %v", parts.test, wantTests)
	}
	if !reflect.DeepEqual(parts.visualLibrary, wantVisualLibraries) {
		t.Errorf("visual libraries = %v, want %v", parts.visualLibrary, wantVisualLibraries)
	}
	if len(parts.bundlerConfigs) != 0 {
		t.Errorf("bundlerConfigs = %v, want empty", parts.bundlerConfigs)
	}
}

func TestExistingRuleOwnedSources(t *testing.T) {
	cfg := newTsConfig()
	c := config.New()
	binary := rule.NewRule(KindTsBinary, "cli")
	binary.SetAttr("srcs", []string{"cli.ts"})
	test := rule.NewRule("js_test", "cli_test")
	test.SetAttr("entry_point", "cli.test.ts")
	resource := rule.NewRule("filegroup", "templates")
	resource.SetAttr("srcs", []string{"template.ts"})
	resource.SetAttr("data", []string{"schema.ts"})
	generated := rule.NewRule(KindTsLibrary, "pkg")
	generated.SetAttr("srcs", []string{"library.ts"})
	generated.SetAttr("data", []string{"generated-resource.ts"})
	generatedTest := rule.NewRule(KindTsTest, "pkg_test")
	generatedTest.SetAttr("data", []string{"stale.test.ts"})
	file := &rule.File{Rules: []*rule.Rule{binary, test, resource, generated, generatedTest}}

	owned := existingRuleOwnedSources(
		c,
		file,
		cfg,
		[]string{
			"cli.ts",
			"cli.test.ts",
			"template.ts",
			"schema.ts",
			"library.ts",
			"generated-resource.ts",
			"stale.test.ts",
		},
		"pkg",
		"pkg_test",
		"pkg_visual_library",
	)

	want := map[string]bool{
		"cli.test.ts": true,
		"template.ts": true,
	}
	if !reflect.DeepEqual(owned, want) {
		t.Fatalf("owned sources = %v, want %v", owned, want)
	}
}

func TestLocalSourcesFromAttr_CompositeExpressions(t *testing.T) {
	file, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
filegroup(
    name = "resources",
    srcs = [":literal.ts"] + glob(
        ["*.template.ts"],
        exclude = ["excluded.template.ts"],
    ) + select({
        "//conditions:default": ["./selected.ts"],
        ":alternate": ["conditional.ts"],
    }),
)
`))
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{
		"conditional.ts":       true,
		"excluded.template.ts": true,
		"literal.ts":           true,
		"owned.template.ts":    true,
		"selected.ts":          true,
		"unrelated.ts":         true,
	}

	got := localSourcesFromAttr(file.Rules[0], "srcs", available)
	want := []string{"conditional.ts", "literal.ts", "owned.template.ts", "selected.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("local sources = %v, want %v", got, want)
	}
}

func TestPartitionedSrcsRemoveOwned(t *testing.T) {
	parts := partitionedSrcs{
		lib:           []string{"cli.ts", "library.ts"},
		test:          []string{"cli.test.ts", "library.test.ts"},
		visualLibrary: []string{"owned.story.tsx", "visible.story.tsx"},
		bundlerConfigs: map[int][]string{
			0: {"owned.config.ts", "visible.config.ts"},
		},
	}
	parts.removeOwned(map[string]bool{
		"cli.ts":          true,
		"cli.test.ts":     true,
		"owned.story.tsx": true,
		"owned.config.ts": true,
	})

	if got, want := parts.lib, []string{"library.ts"}; !reflect.DeepEqual(got, want) {
		t.Errorf("library sources = %v, want %v", got, want)
	}
	if got, want := parts.test, []string{"library.test.ts"}; !reflect.DeepEqual(got, want) {
		t.Errorf("test sources = %v, want %v", got, want)
	}
	if got, want := parts.visualLibrary, []string{"visible.story.tsx"}; !reflect.DeepEqual(got, want) {
		t.Errorf("visual sources = %v, want %v", got, want)
	}
	if got, want := parts.bundlerConfigs[0], []string{"visible.config.ts"}; !reflect.DeepEqual(got, want) {
		t.Errorf("bundler sources = %v, want %v", got, want)
	}
}

func TestPartitionedSrcsPromoteBinarySourcesToLibrary(t *testing.T) {
	parts := partitionedSrcs{
		lib:           []string{"library.ts"},
		test:          []string{"runner.test.ts"},
		visualLibrary: []string{"preview.story.tsx"},
		bundlerConfigs: map[int][]string{
			0: {"tool.config.ts"},
		},
	}
	parts.promoteToLibrary(map[string]bool{
		"runner.test.ts":    true,
		"preview.story.tsx": true,
		"tool.config.ts":    true,
	})

	want := []string{"library.ts", "preview.story.tsx", "runner.test.ts", "tool.config.ts"}
	if !reflect.DeepEqual(parts.lib, want) {
		t.Errorf("library sources = %v, want %v", parts.lib, want)
	}
	if len(parts.test) != 0 || len(parts.visualLibrary) != 0 || len(parts.bundlerConfigs[0]) != 0 {
		t.Errorf("binary sources remained in another bucket: %+v", parts)
	}
}

func TestCollectSrcs_BundlerConfigSplit(t *testing.T) {
	cfg := newTsConfig()
	cfg.bundlerConfigSpecs = []bundlerConfigSpec{
		{Pattern: "vite.config.*", Name: "vite_config"},
		{Pattern: "vitest.config.*", Name: "vitest_config"},
	}
	files := []string{
		"index.ts",
		"vite.config.ts",
		"vitest.config.ts",
		"Button.story.tsx",
		"index.test.ts",
		"helper.ts",
	}
	parts := collectSrcs(files, cfg)

	wantLibs := []string{"helper.ts", "index.ts"}
	if !reflect.DeepEqual(parts.lib, wantLibs) {
		t.Errorf("lib = %v, want %v", parts.lib, wantLibs)
	}
	wantTests := []string{"index.test.ts"}
	if !reflect.DeepEqual(parts.test, wantTests) {
		t.Errorf("test = %v, want %v", parts.test, wantTests)
	}
	wantVisualLibraries := []string{"Button.story.tsx"}
	if !reflect.DeepEqual(parts.visualLibrary, wantVisualLibraries) {
		t.Errorf("visual library = %v, want %v", parts.visualLibrary, wantVisualLibraries)
	}
	if got := parts.bundlerConfigs[0]; !reflect.DeepEqual(got, []string{"vite.config.ts"}) {
		t.Errorf("vite bucket = %v, want [vite.config.ts]", got)
	}
	if got := parts.bundlerConfigs[1]; !reflect.DeepEqual(got, []string{"vitest.config.ts"}) {
		t.Errorf("vitest bucket = %v, want [vitest.config.ts]", got)
	}
}

func TestMatchBundlerConfigSpec_LongestPatternWins(t *testing.T) {
	cfg := newTsConfig()
	cfg.bundlerConfigSpecs = []bundlerConfigSpec{
		// Order is intentional — the more-specific pattern is declared after
		// the broader one, and longest-pattern-wins must override declaration
		// order.
		{Pattern: "vite.config.*", Name: "vite_config"},
		{Pattern: "vite.config.production.ts", Name: "vite_prod_config"},
	}
	idx, ok := matchBundlerConfigSpec("vite.config.production.ts", cfg)
	if !ok || idx != 1 {
		t.Errorf("longest-pattern-wins failed: idx=%d ok=%v", idx, ok)
	}
	idx, ok = matchBundlerConfigSpec("vite.config.ts", cfg)
	if !ok || idx != 0 {
		t.Errorf("broader pattern should match: idx=%d ok=%v", idx, ok)
	}
	if _, ok := matchBundlerConfigSpec("nope.ts", cfg); ok {
		t.Errorf("non-matching file matched")
	}
}

func TestCollectSrcs_BundlerOverridesStory(t *testing.T) {
	cfg := newTsConfig()
	cfg.bundlerConfigSpecs = []bundlerConfigSpec{
		{Pattern: "*.story.tsx", Name: "storybook_config"},
	}
	parts := collectSrcs([]string{"Button.story.tsx", "index.ts"}, cfg)

	if len(parts.visualLibrary) != 0 {
		t.Errorf("visual library bucket should be empty, got %v", parts.visualLibrary)
	}
	if got := parts.bundlerConfigs[0]; !reflect.DeepEqual(got, []string{"Button.story.tsx"}) {
		t.Errorf("bundler bucket = %v, want [Button.story.tsx]", got)
	}
}

func TestCollectSrcs_BundlerOverridesTest(t *testing.T) {
	// A file matching both a test pattern and a bundler-config pattern goes
	// to the bundler-config bucket — the boundary the directive enforces is
	// stronger than the test split.
	cfg := newTsConfig()
	cfg.testPatterns = append(cfg.testPatterns, "*.config.ts")
	cfg.bundlerConfigSpecs = []bundlerConfigSpec{
		{Pattern: "vite.config.ts", Name: "vite_config"},
	}
	parts := collectSrcs([]string{"vite.config.ts", "index.ts"}, cfg)

	if len(parts.test) != 0 {
		t.Errorf("test bucket should be empty, got %v", parts.test)
	}
	if got := parts.bundlerConfigs[0]; !reflect.DeepEqual(got, []string{"vite.config.ts"}) {
		t.Errorf("bundler bucket = %v, want [vite.config.ts]", got)
	}
}

func TestManagedBinaryKinds_IncludesBoth(t *testing.T) {
	// Both launcher kinds should attach to the generated package library.
	want := map[string]bool{KindJsBinary: true, KindTsBinary: true}
	got := map[string]bool{}
	for _, k := range managedBinaryKinds {
		got[k] = true
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("managedBinaryKinds = %v, want %v", got, want)
	}
}

func TestKinds_HasTsBinary(t *testing.T) {
	if _, ok := tsKinds[KindTsBinary]; !ok {
		t.Fatalf("tsKinds missing %q", KindTsBinary)
	}
	info := tsKinds[KindTsBinary]
	if !info.ResolveAttrs["deps"] {
		t.Errorf("ts_binary should have deps as ResolveAttr")
	}
	if info.ResolveAttrs["data"] {
		t.Errorf("ts_binary data should remain user-owned")
	}
	if info.ResolveAttrs["tsconfig_types"] {
		t.Errorf("ts_binary typings should come from its library")
	}
}

func TestKinds_MergeAndResolveAttrsAreDisjoint(t *testing.T) {
	for kind, info := range tsKinds {
		for attr := range info.MergeableAttrs {
			if info.ResolveAttrs[attr] {
				t.Errorf("%s.%s is both mergeable and resolved", kind, attr)
			}
		}
	}

	for _, kind := range []string{KindTsLibrary, KindTsTest, KindTsVisualLibrary, KindBundlerConfig} {
		info := tsKinds[kind]
		if !info.ResolveAttrs["tsconfig_types"] {
			t.Errorf("%s should have tsconfig_types as ResolveAttr", kind)
		}
	}
}

func TestKinds_HasTsVisualLibrary(t *testing.T) {
	info, ok := tsKinds[KindTsVisualLibrary]
	if !ok {
		t.Fatalf("tsKinds missing %q", KindTsVisualLibrary)
	}
	if !info.MergeableAttrs["srcs"] {
		t.Error("ts_visual_library should have srcs as MergeableAttr")
	}
	if !info.ResolveAttrs["deps"] {
		t.Errorf("ts_visual_library should have deps as ResolveAttr")
	}
}

func TestMatchBundlerConfigSpec_DoubleStar(t *testing.T) {
	cfg := newTsConfig()
	cfg.bundlerConfigSpecs = []bundlerConfigSpec{
		{Pattern: "**/main.ts", Name: "storybook_config"},
	}
	if _, ok := matchBundlerConfigSpec(".storybook/main.ts", cfg); !ok {
		t.Errorf("**/main.ts should match .storybook/main.ts")
	}
	if _, ok := matchBundlerConfigSpec("src/main.ts", cfg); !ok {
		t.Errorf("**/main.ts should match src/main.ts")
	}
}
