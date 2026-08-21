package ts

import (
	"reflect"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/resolve"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

func TestImportsIndexesMappedTsLibraryKind(t *testing.T) {
	c := config.New()
	c.Exts[languageName] = &tsConfig{
		extensions: []string{".ts", ".tsx", ".mts"},
	}
	c.KindMap = map[string]config.MappedKind{
		KindTsLibrary: {
			FromKind: KindTsLibrary,
			KindName: "custom_ts_library",
			KindLoad: "//tools:ts.bzl",
		},
	}
	r := rule.NewRule("custom_ts_library", "component-vrt")
	r.SetAttr("srcs", []string{
		"componentVisual.ts",
		"module.mts",
		"nested/view.tsx",
		":generated",
		"//external:src",
		"generated/*.ts",
	})
	f := &rule.File{Pkg: "workspace/frontend/testing/component-vrt"}

	got := (&tsLang{}).Imports(c, r, f)
	want := []resolve.ImportSpec{
		{Lang: languageName, Imp: "workspace/frontend/testing/component-vrt"},
		{Lang: languageName, Imp: "workspace/frontend/testing/component-vrt/*"},
		{Lang: languageName, Imp: "workspace/frontend/testing/component-vrt/componentVisual"},
		{Lang: languageName, Imp: "workspace/frontend/testing/component-vrt/module"},
		{Lang: languageName, Imp: "workspace/frontend/testing/component-vrt/nested/view"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Imports() = %v, want %v", got, want)
	}
}

func TestImportsIndexesCurrentPackageSourceLabels(t *testing.T) {
	c := config.New()
	c.Exts[languageName] = &tsConfig{
		extensions: []string{".ts"},
	}
	r := rule.NewRule(KindTsLibrary, "component-vrt")
	r.SetAttr("srcs", []string{
		":componentVisual.ts",
		":generated",
	})
	f := &rule.File{Pkg: "workspace/frontend/testing/component-vrt"}

	got := (&tsLang{}).Imports(c, r, f)
	want := []resolve.ImportSpec{
		{Lang: languageName, Imp: "workspace/frontend/testing/component-vrt"},
		{Lang: languageName, Imp: "workspace/frontend/testing/component-vrt/*"},
		{Lang: languageName, Imp: "workspace/frontend/testing/component-vrt/componentVisual"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Imports() = %v, want %v", got, want)
	}
}

func TestImportsIndexesKnownSourcesFromPartiallyComputedLists(t *testing.T) {
	c := config.New()
	c.Exts[languageName] = &tsConfig{extensions: []string{".ts"}}
	file, err := rule.LoadData("BUILD.bazel", "workspace/frontend", []byte(`
ts_library(
    name = "frontend",
    srcs = ["literal.ts", GENERATED_SRCS],
)
`))
	if err != nil {
		t.Fatal(err)
	}

	got := (&tsLang{}).Imports(c, file.Rules[0], file)
	want := []resolve.ImportSpec{
		{Lang: languageName, Imp: "workspace/frontend"},
		{Lang: languageName, Imp: "workspace/frontend/*"},
		{Lang: languageName, Imp: "workspace/frontend/literal"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Imports() = %v, want %v", got, want)
	}
}

func TestImportsIndexesConfiguredCompilationOwnersByExactSource(t *testing.T) {
	c := config.New()
	c.Exts[languageName] = &tsConfig{
		extensions:          []string{".ts"},
		sourceProviderKinds: map[string]bool{"ts_project": true},
	}
	r := rule.NewRule("ts_project", "project")
	r.SetAttr("srcs", []string{"project.ts"})
	f := &rule.File{Pkg: "workspace/frontend"}

	got := (&tsLang{}).Imports(c, r, f)
	want := []resolve.ImportSpec{{Lang: languageName, Imp: "workspace/frontend/project"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Imports() = %v, want %v", got, want)
	}
}

func TestImportsIndexesConfiguredProviderIndexAlias(t *testing.T) {
	c := config.New()
	c.Exts[languageName] = &tsConfig{
		extensions:          []string{".ts"},
		sourceProviderKinds: map[string]bool{"ts_project": true},
	}
	file, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
ts_project(
    name = "feature",
    srcs = ["feature/index.ts"],
)
`))
	if err != nil {
		t.Fatal(err)
	}

	got := (&tsLang{}).Imports(c, file.Rules[0], file)
	want := []resolve.ImportSpec{
		{Lang: languageName, Imp: "pkg/feature"},
		{Lang: languageName, Imp: "pkg/feature/index"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Imports() = %v, want %v", got, want)
	}
}

func TestImportsPrefersExactSourceOverIndexAlias(t *testing.T) {
	c := config.New()
	c.Exts[languageName] = &tsConfig{
		extensions:          []string{".ts"},
		sourceProviderKinds: map[string]bool{"ts_project": true},
	}
	file, err := rule.LoadData("BUILD.bazel", "pkg", []byte(`
ts_project(
    name = "feature_index",
    srcs = ["feature/index.ts"],
)

ts_library(
    name = "feature_file",
    srcs = ["feature.ts"],
)
`))
	if err != nil {
		t.Fatal(err)
	}

	got := (&tsLang{}).Imports(c, file.Rules[0], file)
	want := []resolve.ImportSpec{{Lang: languageName, Imp: "pkg/feature/index"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Imports() = %v, want exact feature.ts to suppress the index alias %v", got, want)
	}
}

func TestImportsDoesNotIndexUnconfiguredSourceRules(t *testing.T) {
	c := config.New()
	c.Exts[languageName] = &tsConfig{extensions: []string{".ts"}, sourceProviderKinds: map[string]bool{}}
	r := rule.NewRule("pkg_tar", "archive")
	r.SetAttr("srcs", []string{"resource.ts"})

	if got := (&tsLang{}).Imports(c, r, &rule.File{Pkg: "pkg"}); len(got) != 0 {
		t.Fatalf("Imports() = %v, want resource rule excluded from provider index", got)
	}
}

func TestImportPathForSrcRequiresPackageLocalPath(t *testing.T) {
	c := config.New()
	c.Exts[languageName] = &tsConfig{extensions: []string{".ts"}}
	tests := []struct {
		source string
		want   string
		ok     bool
	}{
		{source: ":local.ts", want: "pkg/local", ok: true},
		{source: "./nested/./local.ts", want: "pkg/nested/local", ok: true},
		{source: "/absolute.ts"},
		{source: "nested/../escaped.ts"},
		{source: "//other:external.ts"},
		{source: "generated/*.ts"},
	}

	for _, test := range tests {
		got, ok := importPathForSrc(c, "pkg", test.source)
		if ok != test.ok || got != test.want {
			t.Errorf("importPathForSrc(%q) = %q, %v; want %q, %v", test.source, got, ok, test.want, test.ok)
		}
	}
}
