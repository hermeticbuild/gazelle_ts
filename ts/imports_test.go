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
	c.KindMap = map[string]config.MappedKind{
		KindTsLibrary: {
			FromKind: KindTsLibrary,
			KindName: "pplx_ts_library",
			KindLoad: "//tools:ts.bzl",
		},
	}
	r := rule.NewRule("pplx_ts_library", "component-vrt")
	f := &rule.File{Pkg: "pplx/frontend/testing/component-vrt"}

	got := (&tsLang{}).Imports(c, r, f)
	want := []resolve.ImportSpec{
		{Lang: languageName, Imp: "pplx/frontend/testing/component-vrt"},
		{Lang: languageName, Imp: "pplx/frontend/testing/component-vrt/*"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Imports() = %v, want %v", got, want)
	}
}
