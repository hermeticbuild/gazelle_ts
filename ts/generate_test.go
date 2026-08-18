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

func TestExistingLibraryName(t *testing.T) {
	c := config.New()
	c.KindMap = map[string]config.MappedKind{}
	c.KindMap[KindTsLibrary] = config.MappedKind{
		FromKind: KindTsLibrary,
		KindName: "custom_ts_library",
	}

	generated := rule.NewRule("custom_ts_library", "typescript-lib")
	artifact := rule.NewRule("js_library", "pkg")
	file := &rule.File{Rules: []*rule.Rule{artifact, generated}}

	if got, want := existingLibraryName(c, file, newTsConfig(), "pkg"), "typescript-lib"; got != want {
		t.Fatalf("library name = %q, want %q", got, want)
	}
}

func TestExistingLibraryNamePreservesExplicitDirective(t *testing.T) {
	cfg := newTsConfig()
	cfg.libraryName = "configured"
	file := &rule.File{Rules: []*rule.Rule{
		rule.NewRule("js_library", "configured"),
		rule.NewRule(KindTsLibrary, "other"),
	}}

	if got := existingLibraryName(config.New(), file, cfg, "configured"); got != "configured" {
		t.Fatalf("library name = %q, want configured", got)
	}
}

func TestExistingLibraryNameUsesCommandConventionForAmbiguousLibraries(t *testing.T) {
	file := &rule.File{Rules: []*rule.Rule{
		rule.NewRule("js_library", "pkg"),
		rule.NewRule(KindTsLibrary, "first"),
		rule.NewRule(KindTsLibrary, "second"),
	}}

	if got := existingLibraryName(config.New(), file, newTsConfig(), "pkg"); got != "pkg_lib" {
		t.Fatalf("library name = %q, want pkg_lib", got)
	}
}

func TestExistingLibraryNameAvoidsBinarySelfDependency(t *testing.T) {
	file := &rule.File{Rules: []*rule.Rule{
		rule.NewRule(KindTsBinary, "pkg"),
	}}

	if got := existingLibraryName(config.New(), file, newTsConfig(), "pkg"); got != "pkg_lib" {
		t.Fatalf("library name = %q, want pkg_lib", got)
	}
}

func TestExistingLibraryNameAvoidsCommandLibraryCollision(t *testing.T) {
	file := &rule.File{Rules: []*rule.Rule{
		rule.NewRule(KindTsBinary, "pkg"),
		rule.NewRule("py_library", "pkg_lib"),
	}}

	if got := existingLibraryName(config.New(), file, newTsConfig(), "pkg"); got != "pkg_lib_2" {
		t.Fatalf("library name = %q, want pkg_lib_2", got)
	}
}

func TestExistingLibraryNamePreservesUnrelatedCollision(t *testing.T) {
	file := &rule.File{Rules: []*rule.Rule{
		rule.NewRule("py_library", "pkg"),
	}}

	if got := existingLibraryName(config.New(), file, newTsConfig(), "pkg"); got != "pkg" {
		t.Fatalf("library name = %q, want pkg", got)
	}
}

func TestBinaryEntryPointInLibrary(t *testing.T) {
	librarySources := map[string]bool{"main.ts": true}
	for _, tc := range []struct {
		name       string
		entryPoint string
		srcs       []string
		want       bool
	}{
		{name: "explicit entry point", entryPoint: "main.ts", srcs: []string{"main.ts"}, want: true},
		{name: "single source fallback", srcs: []string{"main.ts"}, want: true},
		{name: "excluded entry point", entryPoint: "excluded.ts", srcs: []string{"excluded.ts"}},
		{name: "ambiguous sources", srcs: []string{"main.ts", "helper.ts"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := binaryEntryPointInLibrary(tc.entryPoint, tc.srcs, librarySources); got != tc.want {
				t.Fatalf("binaryEntryPointInLibrary() = %v, want %v", got, tc.want)
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
	binary.SetAttr("entry_point", "main.ts")
	customTest := rule.NewRule("js_test", "custom_test")
	customTest.SetAttr("entry_point", "owned.test.ts")
	resource := rule.NewRule("filegroup", "templates")
	resource.SetAttr("srcs", []string{"template.ts"})
	resource.SetAttr("data", []string{"runtime.ts"})
	generated := rule.NewRule(KindTsLibrary, "pkg")
	generated.SetAttr("srcs", []string{"library.ts"})
	file := &rule.File{Rules: []*rule.Rule{binary, customTest, resource, generated}}

	owned := existingRuleOwnedSources(
		c,
		file,
		cfg,
		[]string{"main.ts", "owned.test.ts", "template.ts", "runtime.ts", "library.ts"},
		"pkg",
		"pkg_test",
		"pkg_visual_library",
	)

	want := map[string]bool{
		"owned.test.ts": true,
		"template.ts":   true,
	}
	if !reflect.DeepEqual(owned, want) {
		t.Fatalf("owned sources = %v, want %v", owned, want)
	}
}

func TestLocalSourcesFromAttr(t *testing.T) {
	file, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
filegroup(
    name = "resources",
    srcs = [
        ":literal.ts",
        "./selected.ts",
        "//other:external.ts",
    ],
)
`))
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{
		"literal.ts":   true,
		"selected.ts":  true,
		"unrelated.ts": true,
	}

	got := localSourcesFromAttr(file.Rules[0], "srcs", available)
	want := []string{"literal.ts", "selected.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("local sources = %v, want %v", got, want)
	}
}

func TestPartitionedSrcsRemoveOwned(t *testing.T) {
	parts := partitionedSrcs{
		lib:           []string{"owned.ts", "visible.ts"},
		test:          []string{"owned.test.ts", "visible.test.ts"},
		visualLibrary: []string{"owned.story.tsx", "visible.story.tsx"},
		bundlerConfigs: map[int][]string{
			0: {"owned.config.ts", "visible.config.ts"},
		},
	}
	parts.removeOwned(map[string]bool{
		"owned.ts":        true,
		"owned.test.ts":   true,
		"owned.story.tsx": true,
		"owned.config.ts": true,
	})

	if got, want := parts.lib, []string{"visible.ts"}; !reflect.DeepEqual(got, want) {
		t.Errorf("library sources = %v, want %v", got, want)
	}
	if got, want := parts.test, []string{"visible.test.ts"}; !reflect.DeepEqual(got, want) {
		t.Errorf("test sources = %v, want %v", got, want)
	}
	if got, want := parts.visualLibrary, []string{"visible.story.tsx"}; !reflect.DeepEqual(got, want) {
		t.Errorf("visual sources = %v, want %v", got, want)
	}
	if got, want := parts.bundlerConfigs[0], []string{"visible.config.ts"}; !reflect.DeepEqual(got, want) {
		t.Errorf("bundler sources = %v, want %v", got, want)
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
	// ts_binary and js_binary should both flow through the same data-attr
	// management path. Drift here means the discovery loop in GenerateRules
	// silently skips one of them.
	want := map[string]bool{KindJsBinary: true, KindTsBinary: true}
	got := map[string]bool{}
	for _, k := range managedBinaryKinds {
		got[k] = true
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("managedBinaryKinds = %v, want %v", got, want)
	}
}

func TestBinaryNameForSource(t *testing.T) {
	cfg := newTsConfig()
	for _, tc := range []struct {
		source string
		want   string
	}{
		{source: "main.ts", want: "main_bin"},
		{source: "worker.ts", want: "worker_bin"},
		{source: "nested/worker.tsx", want: "nested_worker_bin"},
	} {
		if got := binaryNameForSource(tc.source, cfg); got != tc.want {
			t.Errorf("binaryNameForSource(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

func TestKinds_HasTsBinary(t *testing.T) {
	// Gazelle manages binary runtime imports and ambient types; generation
	// supplies the sibling-library dependency.
	if _, ok := tsKinds[KindTsBinary]; !ok {
		t.Fatalf("tsKinds missing %q", KindTsBinary)
	}
	info := tsKinds[KindTsBinary]
	if !info.ResolveAttrs["data"] {
		t.Errorf("ts_binary should have data as ResolveAttr")
	}
	if !info.MergeableAttrs["data"] {
		t.Errorf("ts_binary should have data as MergeableAttr")
	}
	for _, attr := range []string{"entry_point", "srcs"} {
		if info.MergeableAttrs[attr] || info.NonEmptyAttrs[attr] {
			t.Errorf("ts_binary %s should remain user-owned", attr)
		}
	}
}

func TestKinds_TsconfigTypesMergeable(t *testing.T) {
	for _, kind := range []string{KindTsLibrary, KindTsTest, KindTsVisualLibrary, KindTsBinary, KindBundlerConfig} {
		info := tsKinds[kind]
		if !info.ResolveAttrs["tsconfig_types"] {
			t.Errorf("%s should have tsconfig_types as ResolveAttr", kind)
		}
		if !info.MergeableAttrs["tsconfig_types"] {
			t.Errorf("%s should have tsconfig_types as MergeableAttr", kind)
		}
	}
}

func TestKinds_HasTsVisualLibrary(t *testing.T) {
	info, ok := tsKinds[KindTsVisualLibrary]
	if !ok {
		t.Fatalf("tsKinds missing %q", KindTsVisualLibrary)
	}
	for _, attr := range []string{"srcs", "deps"} {
		if !info.MergeableAttrs[attr] {
			t.Errorf("ts_visual_library should have %s as MergeableAttr", attr)
		}
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
