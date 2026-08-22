package ts

import (
	"reflect"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

func TestIsCompilationRule(t *testing.T) {
	c := config.New()
	c.KindMap = map[string]config.MappedKind{
		KindTsLibrary: {
			FromKind: KindTsLibrary,
			KindName: "custom_ts_library",
		},
	}

	tests := []struct {
		kind string
		want bool
	}{
		{kind: KindTsLibrary, want: true},
		{kind: "custom_ts_library", want: true},
		{kind: KindTsTest, want: true},
		{kind: KindTsVisualLibrary, want: true},
		{kind: KindBundlerConfig, want: true},
		{kind: KindTsBinary},
		{kind: KindJsBinary},
		{kind: "filegroup"},
		{kind: "tailwind_sources"},
	}

	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			got := isCompilationRule(c, rule.NewRule(test.kind, "target"))
			if got != test.want {
				t.Fatalf("isCompilationRule(%q) = %v, want %v", test.kind, got, test.want)
			}
		})
	}
}

func TestLocalSourcesFromListAttr_IgnoresComputedExpressions(t *testing.T) {
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
		if got, complete := localSourcesFromListAttr(r, "srcs", available); complete {
			t.Errorf("%s sources = %v, true; want computed expression left opaque", r.Name(), got)
		} else if len(got) != 0 {
			t.Errorf("%s sources = %v, false; want no direct literals", r.Name(), got)
		}
	}
	got, complete := localSourcesFromListAttr(file.Rules[3], "srcs", available)
	if want := []string{"literal.ts"}; complete || !reflect.DeepEqual(got, want) {
		t.Errorf("mixed sources = %v, %v; want %v, false", got, complete, want)
	}
}

func TestLiteralStringAttr_RequiresLiteralString(t *testing.T) {
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
	got, literal := literalStringAttr(file.Rules[0], "entry_point")
	if want := "main.ts"; !literal || got != want {
		t.Fatalf("literal entry point = %q, %v; want %q, true", got, literal, want)
	}
	if got, literal := literalStringAttr(file.Rules[1], "entry_point"); literal {
		t.Fatalf("computed entry point = %q, true; want attribute left opaque", got)
	}
}

func TestNormalizeLocalSourceRejectsNonLocalPaths(t *testing.T) {
	tests := []struct {
		source string
		want   string
	}{
		{source: ":local.ts", want: "local.ts"},
		{source: "./nested/local.ts", want: "nested/local.ts"},
		{source: "//other:target"},
		{source: "@repo//pkg:target"},
		{source: "/absolute.ts"},
		{source: "nested/../escaped.ts"},
	}

	for _, test := range tests {
		if got := normalizeLocalSource(test.source); got != test.want {
			t.Errorf("normalizeLocalSource(%q) = %q, want %q", test.source, got, test.want)
		}
	}
}
