package ts

import (
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

	ownership := existingRuleSourceOwnership(
		config.New(),
		file,
		newTsConfig(),
		[]string{"generated.resource.ts", "index.ts", "literal.resource.ts", "selected.resource.ts", "split.ts"},
		"pkg",
		"pkg_test",
		"pkg_visual_library",
	)

	wantClaimed := map[string]bool{
		"generated.resource.ts": true,
		"literal.resource.ts":   true,
		"split.ts":              true,
	}
	if !reflect.DeepEqual(ownership.claimed, wantClaimed) {
		t.Fatalf("claimed sources = %v, want %v", ownership.claimed, wantClaimed)
	}
	wantCompileRoots := map[string]bool{"split.ts": true}
	if !reflect.DeepEqual(ownership.compileRoots, wantCompileRoots) {
		t.Fatalf("compile roots = %v, want %v", ownership.compileRoots, wantCompileRoots)
	}
	wantProviders := map[string]bool{"split.ts": true}
	if !reflect.DeepEqual(ownership.providers, wantProviders) {
		t.Fatalf("providers = %v, want %v", ownership.providers, wantProviders)
	}
}

func TestRemovableOwnedSources_PreservesImportsAndLibraryBackedBinaryEntrypoints(t *testing.T) {
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

	got := lang.removableOwnedSources(
		c,
		"/repo/pkg",
		"pkg",
		[]string{"consumer.ts", "resource.ts", "split.ts", "tool.ts", "unused.ts"},
		ownership,
		refs,
		map[string]bool{"tool.ts": true},
	)
	want := map[string]bool{"split.ts": true, "unused.ts": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("removable sources = %v, want %v", got, want)
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

	for _, r := range file.Rules {
		if got := localSourcesFromAttr(r, "srcs", available); len(got) != 0 {
			t.Errorf("%s sources = %v, want computed expression left unmanaged", r.Name(), got)
		}
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
