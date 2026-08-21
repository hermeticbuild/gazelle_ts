package ts

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

func TestExistingRuleSourceOwnership_ClassifiesCompileAndResourceOwners(t *testing.T) {
	file, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
custom_devserver(
    name = "dev",
    srcs = glob(["**/*.ts"]),
)

custom_compile(
    name = "compiled",
    srcs = ["compiled.ts"],
)

pkg_tar(
    name = "archive",
    srcs = ["archive.ts"],
)

filegroup(
    name = "selected_resources",
    srcs = select({
        "//conditions:default": ["selected.resource.ts"],
    }),
)

filegroup(
    name = "literal_resources",
    srcs = ["literal.resource.ts"],
)

ts_library(
    name = "split",
    srcs = ["split.ts"],
)

ts_library(
    name = "pkg",
    data = ["generated.resource.ts"],
)
`))
	if err != nil {
		t.Fatal(err)
	}

	c := config.New()
	cfg := newTsConfig()
	cfg.sourceProviderKinds["custom_compile"] = true
	c.Exts[languageName] = cfg
	ownership := existingRuleSourceOwnership(
		c,
		file,
		cfg,
		[]string{"archive.ts", "compiled.ts", "generated.resource.ts", "index.ts", "literal.resource.ts", "selected.resource.ts", "split.ts"},
		"pkg",
		"pkg_test",
		"pkg_visual_library",
	)

	wantClaimed := map[string]bool{
		"archive.ts":            true,
		"compiled.ts":           true,
		"generated.resource.ts": true,
		"literal.resource.ts":   true,
		"split.ts":              true,
	}
	if !reflect.DeepEqual(ownership.claimed, wantClaimed) {
		t.Fatalf("claimed sources = %v, want %v", ownership.claimed, wantClaimed)
	}
	wantCompileRoots := map[string]bool{"compiled.ts": true, "split.ts": true}
	if !reflect.DeepEqual(ownership.compileRoots, wantCompileRoots) {
		t.Fatalf("compile roots = %v, want %v", ownership.compileRoots, wantCompileRoots)
	}
	wantProviders := map[string]bool{"compiled.ts": true, "split.ts": true}
	if !reflect.DeepEqual(ownership.providers, wantProviders) {
		t.Fatalf("providers = %v, want %v", ownership.providers, wantProviders)
	}
}

func TestOwnedSourcePlacement_PreservesImportsAndLibraryBackedBinaryEntrypoints(t *testing.T) {
	c := config.New()
	c.RepoRoot = "/repo"
	lang := &tsLang{subpathImportsMap: map[string][]string{
		"#pkg/*": {"./pkg/*"},
	}}
	ownership := sourceOwnership{
		claimed: map[string]bool{
			"resource.ts": true,
			"split.ts":    true,
			"tool.ts":     true,
			"unused.ts":   true,
		},
		compileRoots: map[string]bool{"split.ts": true},
		providers:    map[string]bool{"split.ts": true},
	}
	refs := map[string]ExtractedReferences{
		"/repo/pkg/consumer.ts": {
			Imports: []ImportStatement{
				{ImportPath: "#pkg/resource.js", SourceFile: "/repo/pkg/consumer.ts"},
				{ImportPath: "./split.js", SourceFile: "/repo/pkg/consumer.ts"},
			},
		},
	}

	regularFiles := []string{"consumer.ts", "resource.ts", "split.ts", "tool.ts", "unused.ts"}
	removable, placements := lang.ownedSourcePlacement(
		c,
		"/repo/pkg",
		"pkg",
		regularFiles,
		collectSrcs(regularFiles, newTsConfig()),
		ownership,
		refs,
		map[string]bool{"tool.ts": true},
	)
	wantRemovable := map[string]bool{"split.ts": true, "unused.ts": true}
	if !reflect.DeepEqual(removable, wantRemovable) {
		t.Fatalf("removable sources = %v, want %v", removable, wantRemovable)
	}
	wantPlacements := map[string]sourcePartition{
		"resource.ts": {kind: sourcePartitionLibrary},
		"tool.ts":     {kind: sourcePartitionLibrary},
	}
	if !reflect.DeepEqual(placements, wantPlacements) {
		t.Fatalf("owned source placements = %v, want %v", placements, wantPlacements)
	}
}

func TestOwnedSourcePlacement_FollowsImportingPartition(t *testing.T) {
	tests := []struct {
		name      string
		root      string
		configure func(*tsConfig)
		want      sourcePartition
	}{
		{name: "library", root: "app.ts", want: sourcePartition{kind: sourcePartitionLibrary}},
		{name: "test", root: "app.test.ts", want: sourcePartition{kind: sourcePartitionTest}},
		{name: "visual", root: "Button.story.tsx", want: sourcePartition{kind: sourcePartitionVisual}},
		{
			name: "bundler",
			root: "vite.config.ts",
			configure: func(cfg *tsConfig) {
				cfg.bundlerConfigSpecs = []bundlerConfigSpec{{Pattern: "vite.config.ts", Name: "vite_config"}}
			},
			want: sourcePartition{kind: sourcePartitionBundler, bundlerIndex: 0},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := newTsConfig()
			if test.configure != nil {
				test.configure(cfg)
			}
			c := config.New()
			c.RepoRoot = "/repo"
			regularFiles := []string{test.root, "helper.test.ts"}
			ownership := sourceOwnership{
				claimed:      map[string]bool{"helper.test.ts": true},
				compileRoots: map[string]bool{},
				providers:    map[string]bool{},
			}
			refs := map[string]ExtractedReferences{
				filepath.Join("/repo/pkg", test.root): {
					Imports: []ImportStatement{{ImportPath: "./helper.test.js", SourceFile: filepath.Join("/repo/pkg", test.root)}},
				},
			}

			_, placements := (&tsLang{}).ownedSourcePlacement(
				c,
				"/repo/pkg",
				"pkg",
				regularFiles,
				collectSrcs(regularFiles, cfg),
				ownership,
				refs,
				nil,
			)
			if got := placements["helper.test.ts"]; got != test.want {
				t.Fatalf("helper placement = %v, want %v", got, test.want)
			}
		})
	}
}

func TestLocalSourcesFromAttr_IgnoresComputedExpressions(t *testing.T) {
	file, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
filegroup(
    name = "globbed",
    srcs = glob(["*.template.ts"]),
)

filegroup(
    name = "selected",
    srcs = select({
        "//conditions:default": ["selected.ts"],
    }),
)

filegroup(
    name = "concatenated",
    srcs = ["literal.ts"] + ["additional.ts"],
)

filegroup(
    name = "mixed",
    srcs = ["literal.ts", GENERATED_SRCS],
)
`))
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{
		"additional.ts":     true,
		"literal.ts":        true,
		"owned.template.ts": true,
		"selected.ts":       true,
	}

	for _, r := range file.Rules[:3] {
		if got, ok := localSourcesFromAttr(r, "srcs", available); ok {
			t.Errorf("%s sources = %v, true; want computed expression left unmanaged", r.Name(), got)
		} else if len(got) != 0 {
			t.Errorf("%s sources = %v, false; want no direct literals", r.Name(), got)
		}
	}
	got, ok := localSourcesFromAttr(file.Rules[3], "srcs", available)
	if want := []string{"literal.ts"}; ok || !reflect.DeepEqual(got, want) {
		t.Errorf("mixed sources = %v, %v; want %v, false", got, ok, want)
	}
}

func TestLocalSourcesFromAttr_EntryPointRequiresLiteralString(t *testing.T) {
	file, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
ts_binary(
    name = "literal",
    entry_point = "main.ts",
)

ts_binary(
    name = "computed",
    entry_point = ENTRY_POINT,
)
`))
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{"main.ts": true}

	got, ok := localSourcesFromAttr(file.Rules[0], "entry_point", available)
	if want := []string{"main.ts"}; !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("literal entry point = %v, %v; want %v, true", got, ok, want)
	}
	if got, ok := localSourcesFromAttr(file.Rules[1], "entry_point", available); ok {
		t.Fatalf("computed entry point = %v, true; want attribute left unmanaged", got)
	}
}

func TestHasOpaqueManagedBinarySources(t *testing.T) {
	file, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
ts_binary(
    name = "literal",
    entry_point = "main.ts",
)

ts_binary(
    name = "globbed",
    srcs = glob(["*.ts"]),
)
`))
	if err != nil {
		t.Fatal(err)
	}

	literalOnly := &rule.File{Rules: file.Rules[:1]}
	if hasOpaqueManagedBinarySources(config.New(), literalOnly) {
		t.Fatal("literal binary was classified as opaque")
	}
	if !hasOpaqueManagedBinarySources(config.New(), file) {
		t.Fatal("computed binary sources should suppress automatic binary detection")
	}
}

func TestNewLocalSourceIndex_PrefersExactFileOverIndexAlias(t *testing.T) {
	index := (&tsLang{}).newLocalSourceIndex(
		config.New(),
		"pkg",
		[]string{"feature.ts", "feature/index.ts"},
		map[string]bool{"feature.ts": true, "feature/index.ts": true},
	)

	if got, want := index.byImportPath["pkg/feature"], "feature.ts"; got != want {
		t.Fatalf("source for pkg/feature = %q, want %q", got, want)
	}
}
