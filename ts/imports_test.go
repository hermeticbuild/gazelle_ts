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

func TestImportsBinaryLibraryOnlyIndexesSources(t *testing.T) {
	c := config.New()
	c.Exts[languageName] = &tsConfig{extensions: []string{".ts"}}
	r := rule.NewRule(KindTsLibrary, "cli_bin_lib")
	r.SetAttr("srcs", []string{"main.ts"})
	r.SetPrivateAttr(binaryLibraryKey, true)
	f := &rule.File{Pkg: "apps/cli"}

	got := (&tsLang{}).Imports(c, r, f)
	want := []resolve.ImportSpec{
		{Lang: languageName, Imp: "apps/cli/main"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Imports() = %v, want %v", got, want)
	}
}
