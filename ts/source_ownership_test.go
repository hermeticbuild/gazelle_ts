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
    data = glob(["**/*.ts"]) + ["runtime.ts"],
)

filegroup(
    name = "scanner",
    srcs = glob(["**/*.ts"]) + ["standalone.template.ts"],
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
		[]string{"generated.resource.ts", "index.ts", "runtime.ts", "split.ts", "standalone.template.ts"},
		"pkg",
		"pkg_test",
		"pkg_visual_library",
	)

	wantClaimed := map[string]bool{
		"generated.resource.ts":  true,
		"split.ts":               true,
		"standalone.template.ts": true,
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
