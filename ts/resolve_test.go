package ts

import (
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/label"
	"github.com/bazelbuild/bazel-gazelle/repo"
	gazelleresolve "github.com/bazelbuild/bazel-gazelle/resolve"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

func TestMatchNpmPackage_Bare(t *testing.T) {
	deps := map[string]bool{"react": true, "lodash": true}
	cases := map[string]string{
		"react":             "react",
		"react/jsx-runtime": "react",
		"unknown":           "",
		"lodash/debounce":   "lodash",
	}
	for imp, want := range cases {
		if got := matchNpmPackage(imp, deps); got != want {
			t.Errorf("matchNpmPackage(%q) = %q, want %q", imp, got, want)
		}
	}
}

func TestMatchNpmPackage_Scoped(t *testing.T) {
	deps := map[string]bool{
		"@tanstack/react-query": true,
		"@mui/material":         true,
	}
	cases := map[string]string{
		"@tanstack/react-query":          "@tanstack/react-query",
		"@tanstack/react-query/devtools": "@tanstack/react-query",
		"@mui/material":                  "@mui/material",
		"@unknown/pkg":                   "",
		"@scopeonly":                     "", // missing slash
	}
	for imp, want := range cases {
		if got := matchNpmPackage(imp, deps); got != want {
			t.Errorf("matchNpmPackage(%q) = %q, want %q", imp, got, want)
		}
	}
}

func TestMatchNpmPackage_TypesFallback(t *testing.T) {
	// Type-only imports may resolve via @types/<pkg> when the runtime pkg
	// isn't a direct dep.
	deps := map[string]bool{"@types/lodash": true}
	got := matchNpmPackage("lodash", deps)
	if got != "@types/lodash" {
		t.Errorf("expected @types fallback, got %q", got)
	}
}

func TestTypesPackageFor(t *testing.T) {
	deps := map[string]bool{
		"@types/react":                 true,
		"@types/lodash":                true,
		"@types/tanstack__react-query": true,
	}
	cases := map[string]string{
		"react":                 "@types/react",
		"lodash":                "@types/lodash",
		"@tanstack/react-query": "@types/tanstack__react-query",
		"unknown":               "",
	}
	for pkg, want := range cases {
		if got := typesPackageFor(pkg, deps); got != want {
			t.Errorf("typesPackageFor(%q) = %q, want %q", pkg, got, want)
		}
	}
}

func TestTsconfigTypeForPackage(t *testing.T) {
	cases := map[string]string{
		"@types/node":                  "node",
		"@types/react":                 "react",
		"@types/tanstack__react-query": "tanstack__react-query",
		"react":                        "",
	}
	for pkg, want := range cases {
		if got := tsconfigTypeForPackage(pkg); got != want {
			t.Errorf("tsconfigTypeForPackage(%q) = %q, want %q", pkg, got, want)
		}
	}
}

func TestTsconfigTypePackageForDepLabel(t *testing.T) {
	cases := map[string]string{
		"//:node_modules/@types/node":          "@types/node",
		"@npm//@types/react":                   "@types/react",
		"//deps:node_modules/@types/lodash-es": "@types/lodash-es",
		"//:node_modules/react":                "",
	}
	for dep, want := range cases {
		if got := tsconfigTypePackageForDepLabel(dep); got != want {
			t.Errorf("tsconfigTypePackageForDepLabel(%q) = %q, want %q", dep, got, want)
		}
	}
}

func TestTsconfigTypeForGlobalDepLabel(t *testing.T) {
	defaultCfg := newTsConfig()
	customCfg := newTsConfig()
	customCfg.npmLinkPattern = "//:my_npm/{pkg}"

	cases := []struct {
		dep  string
		cfg  *tsConfig
		want string
	}{
		{dep: "//:node_modules/@types/node", want: "node"},
		{dep: "//:node_modules/@types/chrome", want: "chrome"},
		{dep: "@npm//@types/google.accounts", want: "google.accounts"},
		{dep: "//app/frontend/@types/app-env", want: "app-env"},
		{dep: "//types:custom-global-env", want: "custom-global-env"},
		{dep: "//:node_modules/@cloudflare/workers-types", cfg: defaultCfg, want: "@cloudflare/workers-types"},
		{dep: "//:my_npm/@cloudflare/workers-types", cfg: customCfg, want: "@cloudflare/workers-types"},
	}
	for _, c := range cases {
		if got := tsconfigTypeForGlobalDepLabel(c.dep, c.cfg); got != c.want {
			t.Errorf("tsconfigTypeForGlobalDepLabel(%q) = %q, want %q", c.dep, got, c.want)
		}
	}
}

func TestNpmLabel(t *testing.T) {
	cases := []struct {
		pattern string
		pkg     string
		want    string
	}{
		{"//:node_modules/{pkg}", "react", "//:node_modules/react"},
		{"//:node_modules/{pkg}", "@mui/material", "//:node_modules/@mui/material"},
		{"//pnpm:node_modules/{pkg}", "react", "//pnpm:node_modules/react"},
	}
	for _, c := range cases {
		cfg := &tsConfig{npmLinkPattern: c.pattern}
		got := npmLabel(cfg, c.pkg)
		if got != c.want {
			t.Errorf("npmLabel(%q, %q) = %q, want %q", c.pattern, c.pkg, got, c.want)
		}
	}
}

func TestResolveGlobalsToDeps(t *testing.T) {
	cfg := newTsConfig()
	cfg.globalResolves["process"] = "//:node_modules/@types/node"
	cfg.globalResolves["chrome"] = "//:node_modules/@types/chrome"
	cfg.globalResolves["google"] = "//:node_modules/@types/google"
	cfg.globalResolves["google.accounts"] = "//:node_modules/@types/google.accounts"
	cfg.globalResolves["google.picker"] = "//:node_modules/@types/google.picker"
	cfg.globalResolves["gapi"] = "//:node_modules/@types/gapi"
	cfg.globalResolves["import.meta.env"] = "//app/frontend/@types/app-env"
	cfg.globalResolves["Bar"] = "//:node_modules/@types/bar"
	cfg.globalResolves["R2Bucket"] = "//:node_modules/@cloudflare/workers-types"

	got := resolveGlobalsToDeps(
		[]GlobalReference{
			{Name: "process"},
			{Name: "process"},
			{Name: "chrome"},
			{Name: "google.accounts"},
			{Name: "google.picker.DocumentObject"},
			{Name: "window.gapi.load"},
			{Name: "gapi.load"},
			{Name: "window.unconfigured.load"},
			{Name: "import.meta.env"},
			{Name: "Bar"},
			{Name: "R2Bucket"},
		},
		cfg,
	)

	if want := []string{"//:node_modules/@cloudflare/workers-types", "//:node_modules/@types/bar", "//:node_modules/@types/chrome", "//:node_modules/@types/gapi", "//:node_modules/@types/google.accounts", "//:node_modules/@types/google.picker", "//:node_modules/@types/node", "//app/frontend/@types/app-env"}; !reflect.DeepEqual(got.external, want) {
		t.Errorf("external = %v, want %v", got.external, want)
	}
	if want := []string{"@cloudflare/workers-types", "app-env", "bar", "chrome", "gapi", "google.accounts", "google.picker", "node"}; !reflect.DeepEqual(got.tsconfigTypes, want) {
		t.Errorf("tsconfigTypes = %v, want %v", got.tsconfigTypes, want)
	}
}

func TestResolve_GlobalScopedNpmTypePackageAddsTsconfigTypes(t *testing.T) {
	cfg := newTsConfig()
	cfg.globalResolves["R2Bucket"] = "//:node_modules/@cloudflare/workers-types"

	c := config.New()
	c.Exts[languageName] = cfg
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", nil)

	lang := &tsLang{
		packageDeps:       map[string]bool{"@cloudflare/workers-types": true},
		subpathImportsMap: map[string][]string{},
	}
	r := rule.NewRule(KindTsLibrary, "worker")

	lang.Resolve(
		c,
		nil,
		nil,
		r,
		ImportData{
			Globals: []GlobalReference{{Name: "R2Bucket"}},
		},
		label.Label{Pkg: "apps/worker", Name: "worker"},
	)

	wantDeps := []string{"//:node_modules/@cloudflare/workers-types"}
	if got := r.AttrStrings("deps"); !reflect.DeepEqual(got, wantDeps) {
		t.Errorf("deps = %v, want %v", got, wantDeps)
	}

	wantTypes := []string{"@cloudflare/workers-types"}
	if got := r.AttrStrings("tsconfig_types"); !reflect.DeepEqual(got, wantTypes) {
		t.Errorf("tsconfig_types = %v, want %v", got, wantTypes)
	}
}

func TestResolveImportsToDeps_TsconfigTypes(t *testing.T) {
	cfg := newTsConfig()
	c := config.New()
	c.Exts[languageName] = cfg
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", nil)
	lang := &tsLang{
		packageDeps: map[string]bool{
			"react":                  true,
			"@types/react":           true,
			"@types/node":            true,
			"@types/lodash":          true,
			"@types/ws":              true,
			"@tanstack/query":        true,
			"@types/tanstack__query": true,
		},
		subpathImportsMap: map[string][]string{},
	}
	got := lang.resolveImportsToDeps(
		c,
		[]ImportStatement{
			{ImportPath: "node:fs"},
			{ImportPath: "react"},
			{ImportPath: "lodash"},
			{ImportPath: "@tanstack/query"},
		},
		label.Label{Pkg: "apps/web", Name: "web"},
		nil,
		cfg,
	)
	want := []string{"node"}
	if !reflect.DeepEqual(got.tsconfigTypes, want) {
		t.Errorf("tsconfigTypes = %v, want %v", got.tsconfigTypes, want)
	}
}

func TestResolveImportsToDeps_TsconfigTypesFromOverride(t *testing.T) {
	cfg := newTsConfig()
	c := config.New()
	c.Exts[languageName] = cfg
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", &rule.File{
		Directives: []rule.Directive{
			ruleDirective("resolve", "ts runtime-only //:node_modules/@types/node"),
		},
	})

	lang := &tsLang{
		packageDeps:       map[string]bool{},
		subpathImportsMap: map[string][]string{},
	}
	got := lang.resolveImportsToDeps(
		c,
		[]ImportStatement{{ImportPath: "runtime-only"}},
		label.Label{Pkg: "apps/web", Name: "web"},
		nil,
		cfg,
	)
	if !reflect.DeepEqual(got.tsconfigTypes, []string{"node"}) {
		t.Errorf("tsconfigTypes = %v, want [node]", got.tsconfigTypes)
	}
}

func TestResolve_TsconfigTypesDirectiveAllowlistsInference(t *testing.T) {
	cfg := newTsConfig()
	cfg.tsconfigTypes = []string{"node", "vitest"}
	c := config.New()
	c.Exts[languageName] = cfg
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", nil)

	lang := &tsLang{
		packageDeps:       map[string]bool{"@types/vitest": true},
		subpathImportsMap: map[string][]string{},
	}
	r := rule.NewRule(KindTsLibrary, "lib")
	lang.Resolve(
		c,
		nil,
		nil,
		r,
		ImportData{Imports: []ImportStatement{{ImportPath: "vitest"}}},
		label.Label{Pkg: "apps/web", Name: "web"},
	)
	want := []string{"vitest"}
	if got := r.AttrStrings("tsconfig_types"); !reflect.DeepEqual(got, want) {
		t.Errorf("tsconfig_types = %v, want %v", got, want)
	}
}

func TestResolve_TsLibraryUsesResolvedGlobals(t *testing.T) {
	cfg := newTsConfig()
	cfg.globalResolves["process"] = "//:node_modules/@types/node"
	cfg.globalResolves["chrome"] = "//:node_modules/@types/chrome"
	cfg.globalResolves["google.accounts"] = "//:node_modules/@types/google.accounts"
	cfg.globalResolves["import.meta.env"] = "//app/frontend/@types/import-meta-env"
	cfg.globalResolves["appEnv"] = "//app/frontend/@types/app-env"
	cfg.globalResolves["R2Bucket"] = "//:node_modules/@cloudflare/workers-types"
	c := config.New()
	c.Exts[languageName] = cfg
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", nil)

	lang := &tsLang{
		packageDeps:       map[string]bool{},
		subpathImportsMap: map[string][]string{},
	}
	r := rule.NewRule(KindTsLibrary, "lib")
	lang.Resolve(
		c,
		nil,
		nil,
		r,
		ImportData{
			Globals: []GlobalReference{
				{Name: "process"},
				{Name: "chrome"},
				{Name: "google.accounts"},
				{Name: "import.meta.env"},
				{Name: "appEnv"},
				{Name: "R2Bucket"},
			},
		},
		label.Label{Pkg: "apps/web", Name: "web"},
	)

	wantDeps := []string{
		"//:node_modules/@cloudflare/workers-types",
		"//:node_modules/@types/chrome",
		"//:node_modules/@types/google.accounts",
		"//:node_modules/@types/node",
		"//app/frontend/@types/app-env",
		"//app/frontend/@types/import-meta-env",
	}
	if got := r.AttrStrings("deps"); !reflect.DeepEqual(got, wantDeps) {
		t.Errorf("deps = %v, want %v", got, wantDeps)
	}
	wantTypes := []string{"@cloudflare/workers-types", "app-env", "chrome", "google.accounts", "import-meta-env", "node"}
	if got := r.AttrStrings("tsconfig_types"); !reflect.DeepEqual(got, wantTypes) {
		t.Errorf("tsconfig_types = %v, want %v", got, wantTypes)
	}
}

func TestResolve_MappedTsLibraryPopulatesTsconfigTypes(t *testing.T) {
	cfg := newTsConfig()
	c := config.New()
	c.Exts[languageName] = cfg
	c.KindMap = map[string]config.MappedKind{}
	c.KindMap[KindTsLibrary] = config.MappedKind{
		FromKind: KindTsLibrary,
		KindName: "myorg_ts_library",
		KindLoad: "//tools:ts.bzl",
	}
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", nil)

	lang := &tsLang{
		packageDeps:       map[string]bool{"@types/node": true},
		subpathImportsMap: map[string][]string{},
	}
	r := rule.NewRule("myorg_ts_library", "lib")
	lang.Resolve(
		c,
		nil,
		nil,
		r,
		ImportData{Imports: []ImportStatement{{ImportPath: "node:path"}}},
		label.Label{Pkg: "apps/web", Name: "web"},
	)

	if got, want := r.AttrStrings("tsconfig_types"), []string{"node"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tsconfig_types = %v, want %v", got, want)
	}
	if got, want := r.AttrStrings("deps"), []string{"//:node_modules/@types/node"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestResolve_MappedTsTestPopulatesTsconfigTypes(t *testing.T) {
	cfg := newTsConfig()
	cfg.tsconfigTypes = []string{"node", "vitest"}
	c := config.New()
	c.Exts[languageName] = cfg
	c.KindMap = map[string]config.MappedKind{}
	c.KindMap[KindTsTest] = config.MappedKind{
		FromKind: KindTsTest,
		KindName: "vitest_test",
		KindLoad: "//tools:ts.bzl",
	}
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", nil)

	lang := &tsLang{
		packageDeps:       map[string]bool{"@types/node": true, "@types/vitest": true},
		subpathImportsMap: map[string][]string{},
	}
	r := rule.NewRule("vitest_test", "test")
	r.SetAttr("srcs", []string{"app.test.ts"})
	r.SetAttr("data", []string{"fixtures/sample.json"})
	r.SetAttr("deps", []string{":web"})
	lang.Resolve(
		c,
		nil,
		nil,
		r,
		ImportData{
			TestImports: []ImportStatement{{ImportPath: "node:path"}, {ImportPath: "vitest"}},
		},
		label.Label{Pkg: "apps/web", Name: "web_test"},
	)

	if got, want := r.AttrStrings("tsconfig_types"), []string{"node", "vitest"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tsconfig_types = %v, want %v", got, want)
	}
	if got, want := r.AttrStrings("srcs"), []string{"app.test.ts"}; !reflect.DeepEqual(got, want) {
		t.Errorf("srcs = %v, want %v", got, want)
	}
	if got, want := r.AttrStrings("deps"), []string{"//:node_modules/@types/node", "//:node_modules/@types/vitest", ":web"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
	if got, want := r.AttrStrings("data"), []string{"fixtures/sample.json"}; !reflect.DeepEqual(got, want) {
		t.Errorf("data = %v, want %v", got, want)
	}
}

func TestResolve_MappedTsVisualLibraryPopulatesDepsAndTsconfigTypes(t *testing.T) {
	cfg := newTsConfig()
	c := config.New()
	c.Exts[languageName] = cfg
	c.KindMap = map[string]config.MappedKind{}
	c.KindMap[KindTsVisualLibrary] = config.MappedKind{
		FromKind: KindTsVisualLibrary,
		KindName: "myorg_ts_visual_library",
		KindLoad: "//tools:ts.bzl",
	}
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", nil)

	lang := &tsLang{
		packageDeps:       map[string]bool{"@storybook/react": true, "@types/node": true},
		subpathImportsMap: map[string][]string{},
	}
	r := rule.NewRule("myorg_ts_visual_library", "web_visual_library")
	r.SetAttr("srcs", []string{"Button.story.tsx"})
	r.SetAttr("deps", []string{":web"})
	lang.Resolve(
		c,
		nil,
		nil,
		r,
		ImportData{
			Imports: []ImportStatement{
				{ImportPath: "@storybook/react"},
				{ImportPath: "node:path"},
			},
		},
		label.Label{Pkg: "apps/web", Name: "web_visual_library"},
	)

	if got, want := r.AttrStrings("tsconfig_types"), []string{"node"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tsconfig_types = %v, want %v", got, want)
	}
	if got, want := r.AttrStrings("srcs"), []string{"Button.story.tsx"}; !reflect.DeepEqual(got, want) {
		t.Errorf("srcs = %v, want %v", got, want)
	}
	if got, want := r.AttrStrings("deps"), []string{"//:node_modules/@storybook/react", "//:node_modules/@types/node", ":web"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestResolve_TsTestPreservesExplicitTsconfigTypes(t *testing.T) {
	cfg := newTsConfig()
	c := config.New()
	c.Exts[languageName] = cfg
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", nil)

	lang := &tsLang{subpathImportsMap: map[string][]string{}}
	r := rule.NewRule(KindTsTest, "test")
	r.SetAttr("srcs", []string{"app.test.ts"})
	r.SetAttr("deps", []string{"//:node_modules/@types/node"})
	r.SetAttr("tsconfig_types", []string{"node"})

	lang.Resolve(
		c,
		nil,
		nil,
		r,
		ImportData{},
		label.Label{Pkg: "apps/web", Name: "web_test"},
	)

	if got, want := r.AttrStrings("srcs"), []string{"app.test.ts"}; !reflect.DeepEqual(got, want) {
		t.Errorf("srcs = %v, want %v", got, want)
	}
	if got, want := r.AttrStrings("deps"), []string{"//:node_modules/@types/node"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
	if got, want := r.AttrStrings("tsconfig_types"), []string{"node"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tsconfig_types = %v, want %v", got, want)
	}
}

func TestResolve_MappedTsBinaryPopulatesTsconfigTypes(t *testing.T) {
	cfg := newTsConfig()
	c := config.New()
	c.Exts[languageName] = cfg
	c.KindMap = map[string]config.MappedKind{}
	c.KindMap[KindTsBinary] = config.MappedKind{
		FromKind: KindTsBinary,
		KindName: "myorg_ts_binary",
		KindLoad: "//tools:ts.bzl",
	}
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", nil)

	lang := &tsLang{
		packageDeps:       map[string]bool{"@types/node": true},
		subpathImportsMap: map[string][]string{},
	}
	r := rule.NewRule("myorg_ts_binary", "cli")
	r.SetAttr("data", []string{"runtime.json"})
	lang.Resolve(
		c,
		nil,
		nil,
		r,
		ImportData{Imports: []ImportStatement{{ImportPath: "node:fs"}}},
		label.Label{Pkg: "apps/cli", Name: "cli"},
	)

	if got, want := r.AttrStrings("tsconfig_types"), []string{"node"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tsconfig_types = %v, want %v", got, want)
	}
	if got, want := r.AttrStrings("data"), []string{"//:node_modules/@types/node", "runtime.json"}; !reflect.DeepEqual(got, want) {
		t.Errorf("data = %v, want %v", got, want)
	}
}

func TestMatchSubpathImportPattern_NonSuffixWildcard(t *testing.T) {
	capture, ok := matchSubpathImportPattern("#generated/typespec/rest/*/index.js", "#generated/typespec/rest/users/index.js")
	if !ok {
		t.Fatalf("matchSubpathImportPattern did not match")
	}
	if capture != "users" {
		t.Errorf("capture = %q, want users", capture)
	}

	if _, ok := matchSubpathImportPattern("#generated/typespec/rest/*/index.js", "#generated/typespec/rest/users/client.js"); ok {
		t.Errorf("unexpected match for non-index import")
	}
}

func TestMatchSubpathImportPattern_NodeStarCanContainSlash(t *testing.T) {
	capture, ok := matchSubpathImportPattern("#generated/protobuf/*.js", "#generated/protobuf/foo/bar/baz.js")
	if !ok {
		t.Fatalf("matchSubpathImportPattern did not match")
	}
	if capture != "foo/bar/baz" {
		t.Errorf("capture = %q, want foo/bar/baz", capture)
	}
}

func TestResolveSubpathImport_LabelTemplateFromPackageImports(t *testing.T) {
	lang := &tsLang{
		packageDeps: map[string]bool{},
		subpathImportsMap: map[string][]string{
			"#generated/typespec/rest/*/index.js": {"//typespec/rest/*:*.web"},
		},
	}

	got, external := lang.resolveSubpathImport(
		nil,
		"#generated/typespec/rest/users/index.js",
		label.Label{Pkg: "apps/web", Name: "web"},
		nil,
	)
	if !external {
		t.Fatalf("external = false, want true")
	}
	if got != "//typespec/rest/users:users.web" {
		t.Errorf("resolveSubpathImport = %q, want //typespec/rest/users:users.web", got)
	}
}

func TestResolveSubpathImport_PathTargetUsesRuleIndex(t *testing.T) {
	lang := &tsLang{
		packageDeps: map[string]bool{},
		subpathImportsMap: map[string][]string{
			"#generated/typespec/rest/*/index.js": {"./typespec/rest/*"},
		},
	}
	c := config.New()
	c.Exts[languageName] = newTsConfig()
	ix := gazelleresolve.NewRuleIndex(func(r *rule.Rule, pkgRel string) gazelleresolve.Resolver {
		if r.Kind() == KindTsLibrary {
			return lang
		}
		return nil
	})
	ix.AddRule(c, rule.NewRule(KindTsLibrary, "users.web"), &rule.File{Pkg: "typespec/rest/users"})
	ix.Finish()

	got, external := lang.resolveSubpathImport(
		c,
		"#generated/typespec/rest/users/index.js",
		label.Label{Pkg: "app", Name: "app"},
		ix,
	)
	if external {
		t.Fatalf("external = true, want false")
	}
	if got != "//typespec/rest/users:users.web" {
		t.Errorf("resolveSubpathImport = %q, want //typespec/rest/users:users.web", got)
	}
}

func TestResolveSubpathImport_PathTargetUsesPackageWildcard(t *testing.T) {
	lang := &tsLang{
		packageDeps: map[string]bool{},
		subpathImportsMap: map[string][]string{
			"#workspace/*": {"./workspace/*"},
		},
	}
	c := config.New()
	c.Exts[languageName] = newTsConfig()
	ix := gazelleresolve.NewRuleIndex(func(r *rule.Rule, pkgRel string) gazelleresolve.Resolver {
		if r.Kind() == KindTsLibrary {
			return lang
		}
		return nil
	})
	ix.AddRule(c, rule.NewRule(KindTsLibrary, "component-vrt"), &rule.File{Pkg: "workspace/frontend/testing/component-vrt"})
	ix.Finish()

	got, external := lang.resolveSubpathImport(
		c,
		"#workspace/frontend/testing/component-vrt/componentVisual.js",
		label.Label{Pkg: "workspace/frontend/ui/components/Button", Name: "visual_module"},
		ix,
	)
	if external {
		t.Fatalf("external = true, want false")
	}
	if got != "//workspace/frontend/testing/component-vrt" {
		t.Errorf("resolveSubpathImport = %q, want //workspace/frontend/testing/component-vrt", got)
	}
}

func TestResolveSubpathImport_ExactSourceOwnerWinsOverPackageAggregate(t *testing.T) {
	lang := &tsLang{
		packageDeps: map[string]bool{},
		subpathImportsMap: map[string][]string{
			"#workspace/*": {"./workspace/*"},
		},
	}
	c := config.New()
	cfg := newTsConfig()
	cfg.extensions = append(cfg.extensions, ".mts")
	c.Exts[languageName] = cfg
	c.KindMap = map[string]config.MappedKind{
		KindTsLibrary: {
			FromKind: KindTsLibrary,
			KindName: "custom_ts_library",
		},
	}
	ix := gazelleresolve.NewRuleIndex(func(r *rule.Rule, pkgRel string) gazelleresolve.Resolver {
		if kindMatches(c, r.Kind(), KindTsLibrary) {
			return lang
		}
		return nil
	})

	aggregate := rule.NewRule("custom_ts_library", "component-vrt")
	aggregate.SetAttr("srcs", []string{"browserChannelBridge.ts"})
	componentVisual := rule.NewRule("custom_ts_library", "component-visual")
	componentVisual.SetAttr("srcs", []string{"componentVisual.mts"})
	file := &rule.File{Pkg: "workspace/frontend/testing/component-vrt"}
	ix.AddRule(c, aggregate, file)
	ix.AddRule(c, componentVisual, file)
	ix.Finish()

	got, external := lang.resolveSubpathImport(
		c,
		"#workspace/frontend/testing/component-vrt/componentVisual.mjs",
		label.Label{Pkg: "workspace/frontend/ui/components/Button", Name: "visual_module"},
		ix,
	)
	if external {
		t.Fatalf("external = true, want false")
	}
	if got != "//workspace/frontend/testing/component-vrt:component-visual" {
		t.Errorf("resolveSubpathImport = %q, want //workspace/frontend/testing/component-vrt:component-visual", got)
	}
}

func TestResolveSubpathImport_SamePackageExactSourceOwner(t *testing.T) {
	lang := &tsLang{
		packageDeps: map[string]bool{},
		subpathImportsMap: map[string][]string{
			"#workspace/*": {"./workspace/*"},
		},
	}
	c := config.New()
	cfg := newTsConfig()
	cfg.extensions = append(cfg.extensions, ".mts")
	c.Exts[languageName] = cfg
	ix := gazelleresolve.NewRuleIndex(func(r *rule.Rule, pkgRel string) gazelleresolve.Resolver {
		if r.Kind() == KindTsLibrary {
			return lang
		}
		return nil
	})

	componentVisual := rule.NewRule(KindTsLibrary, "component-visual")
	componentVisual.SetAttr("srcs", []string{"componentVisual.mts"})
	file := &rule.File{Pkg: "workspace/frontend/testing/component-vrt"}
	ix.AddRule(c, componentVisual, file)
	ix.Finish()

	got, external := lang.resolveSubpathImport(
		c,
		"#workspace/frontend/testing/component-vrt/componentVisual.mjs",
		label.Label{Pkg: "workspace/frontend/testing/component-vrt", Name: "visual_module"},
		ix,
	)
	if external {
		t.Fatalf("external = true, want false")
	}
	if got != ":component-visual" {
		t.Errorf("resolveSubpathImport = %q, want :component-visual", got)
	}
}

func TestResolveImportsToDeps_RelativeParentPackageImportUsesRuleIndex(t *testing.T) {
	lang := &tsLang{
		packageDeps:       map[string]bool{},
		subpathImportsMap: map[string][]string{},
	}
	c := config.New()
	c.RepoRoot = "/repo"
	c.Exts[languageName] = newTsConfig()
	ix := gazelleresolve.NewRuleIndex(func(r *rule.Rule, pkgRel string) gazelleresolve.Resolver {
		if r.Kind() == KindTsLibrary {
			return lang
		}
		return nil
	})
	ix.AddRule(c, rule.NewRule(KindTsLibrary, "lib"), &rule.File{Pkg: "pkg"})
	ix.Finish()

	got := lang.resolveImportsToDeps(
		c,
		[]ImportStatement{{
			ImportPath: "../src",
			SourceFile: "/repo/pkg/tests/foo.spec.ts",
		}},
		label.Label{Pkg: "pkg/tests", Name: "tests_test"},
		ix,
		newTsConfig(),
	)

	if !reflect.DeepEqual(got.internal, []string{"//pkg:lib"}) {
		t.Errorf("internal deps = %v, want [//pkg:lib]", got.internal)
	}
}

func TestResolveImportsToDeps_RelativeSamePackageImportIgnored(t *testing.T) {
	lang := &tsLang{
		packageDeps:       map[string]bool{},
		subpathImportsMap: map[string][]string{},
	}
	c := config.New()
	c.RepoRoot = "/repo"
	c.Exts[languageName] = newTsConfig()
	ix := gazelleresolve.NewRuleIndex(func(r *rule.Rule, pkgRel string) gazelleresolve.Resolver {
		if r.Kind() == KindTsLibrary {
			return lang
		}
		return nil
	})
	ix.AddRule(c, rule.NewRule(KindTsLibrary, "tests"), &rule.File{Pkg: "pkg/tests"})
	ix.AddRule(c, rule.NewRule(KindTsLibrary, "lib"), &rule.File{Pkg: "pkg"})
	ix.Finish()

	got := lang.resolveImportsToDeps(
		c,
		[]ImportStatement{{
			ImportPath: "./helper",
			SourceFile: "/repo/pkg/tests/foo.spec.ts",
		}},
		label.Label{Pkg: "pkg/tests", Name: "tests_test"},
		ix,
		newTsConfig(),
	)

	if len(got.internal) != 0 {
		t.Errorf("internal deps = %v, want empty same-package import", got.internal)
	}
}

func TestResolveSubpathImport_LongestIndexedPackageWins(t *testing.T) {
	lang := &tsLang{
		packageDeps: map[string]bool{},
		subpathImportsMap: map[string][]string{
			"#apps/*": {"./apps/*"},
		},
	}
	importsByName := map[string][]gazelleresolve.ImportSpec{
		"web": {
			{Lang: languageName, Imp: "apps/web/lib/auth/permissions"},
		},
		"auth": {
			{Lang: languageName, Imp: "apps/web/lib/auth/permissions"},
		},
	}
	resolver := staticImportResolver{importsByName: importsByName}
	c := config.New()
	ix := gazelleresolve.NewRuleIndex(func(r *rule.Rule, pkgRel string) gazelleresolve.Resolver {
		return resolver
	})
	ix.AddRule(c, rule.NewRule("fake_ts_library", "web"), &rule.File{Pkg: "apps/web"})
	ix.AddRule(c, rule.NewRule("fake_ts_library", "auth"), &rule.File{Pkg: "apps/web/lib/auth"})
	ix.Finish()

	got, external := lang.resolveSubpathImport(
		c,
		"#apps/web/lib/auth/permissions.js",
		label.Label{Pkg: "apps/web", Name: "web"},
		ix,
	)
	if external {
		t.Fatalf("external = true, want false")
	}
	if got != "//apps/web/lib/auth" {
		t.Errorf("resolveSubpathImport = %q, want //apps/web/lib/auth", got)
	}
}

func TestResolveSubpathImport_SuppressesSameRuleAlias(t *testing.T) {
	lang := &tsLang{
		packageDeps: map[string]bool{},
		subpathImportsMap: map[string][]string{
			"#repo/*": {"./*"},
		},
	}
	importsByName := map[string][]gazelleresolve.ImportSpec{
		"widgets": {
			{Lang: languageName, Imp: "path/widgets/shader"},
		},
	}
	resolver := staticImportResolver{importsByName: importsByName}
	c := config.New()
	ix := gazelleresolve.NewRuleIndex(func(r *rule.Rule, pkgRel string) gazelleresolve.Resolver {
		return resolver
	})
	ix.AddRule(c, rule.NewRule("fake_ts_library", "widgets"), &rule.File{Pkg: "path/widgets"})
	ix.Finish()

	got, external := lang.resolveSubpathImport(
		c,
		"#repo/path/widgets/shader.js",
		label.Label{Pkg: "path/widgets", Name: "widgets"},
		ix,
	)
	if external {
		t.Fatalf("external = true, want false")
	}
	if got != "" {
		t.Errorf("resolveSubpathImport = %q, want empty self-dep", got)
	}
}

func TestResolveSubpathImport_DoesNotFallbackToParentForSameRuleAlias(t *testing.T) {
	lang := &tsLang{
		packageDeps: map[string]bool{},
		subpathImportsMap: map[string][]string{
			"#repo/*": {"./*"},
		},
	}
	importsByName := map[string][]gazelleresolve.ImportSpec{
		"parent": {
			{Lang: languageName, Imp: "path/widgets/shaders/shader"},
		},
		"shaders": {
			{Lang: languageName, Imp: "path/widgets/shaders/shader"},
		},
	}
	resolver := staticImportResolver{importsByName: importsByName}
	c := config.New()
	ix := gazelleresolve.NewRuleIndex(func(r *rule.Rule, pkgRel string) gazelleresolve.Resolver {
		return resolver
	})
	ix.AddRule(c, rule.NewRule("fake_ts_library", "parent"), &rule.File{Pkg: "path/widgets"})
	ix.AddRule(c, rule.NewRule("fake_ts_library", "shaders"), &rule.File{Pkg: "path/widgets/shaders"})
	ix.Finish()

	got, external := lang.resolveSubpathImport(
		c,
		"#repo/path/widgets/shaders/shader.js",
		label.Label{Pkg: "path/widgets/shaders", Name: "shaders"},
		ix,
	)
	if external {
		t.Fatalf("external = true, want false")
	}
	if got != "" {
		t.Errorf("resolveSubpathImport = %q, want empty same-rule import", got)
	}
}

func TestResolveImportsToDeps_ExactOverridePrecedesPackageImports(t *testing.T) {
	cfg := newTsConfig()
	c := config.New()
	c.Exts[languageName] = cfg
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", &rule.File{
		Directives: []rule.Directive{
			ruleDirective("resolve", "ts #generated/types/user.js //exact:dep"),
		},
	})

	lang := &tsLang{
		packageDeps: map[string]bool{},
		subpathImportsMap: map[string][]string{
			"#generated/*": {"//generated:*"},
		},
	}
	got := lang.resolveImportsToDeps(
		c,
		[]ImportStatement{{ImportPath: "#generated/types/user.js"}},
		label.Label{Pkg: "apps/web", Name: "web"},
		nil,
		cfg,
	)
	want := []string{"//exact:dep"}
	if !reflect.DeepEqual(got.external, want) {
		t.Errorf("external deps = %v, want %v", got.external, want)
	}
}

func TestResolveImportsToDeps_RegexpOverride(t *testing.T) {
	cfg := newTsConfig()
	c := config.New()
	c.Exts[languageName] = cfg
	resolveConfigurer := &gazelleresolve.Configurer{}
	resolveConfigurer.RegisterFlags(flag.NewFlagSet("test", flag.ContinueOnError), "", c)
	resolveConfigurer.Configure(c, "", &rule.File{
		Directives: []rule.Directive{
			ruleDirective("resolve_regexp", `ts ^@myrepo_generated/(.*)$ //:node_modules/@myrepo_generated/$1`),
		},
	})

	lang := &tsLang{
		packageDeps:       map[string]bool{},
		subpathImportsMap: map[string][]string{},
	}
	got := lang.resolveImportsToDeps(
		c,
		[]ImportStatement{{ImportPath: "@myrepo_generated/synthetic"}},
		label.Label{Pkg: "apps/cli", Name: "cli"},
		nil,
		cfg,
	)
	want := []string{"//:node_modules/@myrepo_generated/synthetic"}
	if !reflect.DeepEqual(got.external, want) {
		t.Errorf("external deps = %v, want %v", got.external, want)
	}
}

func TestLoadPackageJSONDeps_ArrayFallbackImports(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/package.json", []byte(`{
  "dependencies": {
    "react": "latest"
  },
  "imports": {
    "#generated/foo/*": [
      "./bazel-bin/generated/foo/dist/*",
      "./generated/foo/dist/*"
    ],
    "#conditional/*": {
      "browser": "./browser/*",
      "node": {
        "require": "./node-cjs/*",
        "import": "./node-esm/*"
      },
      "default": "./default/*"
    },
    "#null/*": null,
    "#unsupported/*": [42, null]
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	lang := &tsLang{
		packageDeps:       map[string]bool{},
		subpathImportsMap: map[string][]string{},
	}
	lang.loadPackageJSONDeps(dir)

	if !lang.packageDeps["react"] {
		t.Fatalf("dependencies were not loaded")
	}
	want := []string{
		"./bazel-bin/generated/foo/dist/*",
		"./generated/foo/dist/*",
	}
	if got := lang.subpathImportsMap["#generated/foo/*"]; !reflect.DeepEqual(got, want) {
		t.Errorf("imports fallback targets = %v, want %v", got, want)
	}
	if got := lang.subpathImportsMap["#conditional/*"]; !reflect.DeepEqual(got, []string{"./node-esm/*"}) {
		t.Errorf("conditional imports targets = %v, want [./node-esm/*]", got)
	}
	if _, ok := lang.subpathImportsMap["#null/*"]; ok {
		t.Errorf("null imports entry should be ignored")
	}
	if _, ok := lang.subpathImportsMap["#unsupported/*"]; ok {
		t.Errorf("unsupported imports entry should be ignored")
	}
}

func TestDecodePackageImportTargets_ConditionOrder(t *testing.T) {
	raw := json.RawMessage(`{
  "default": "./default.js",
  "node": "./node.js"
}`)
	got := decodePackageImportTargets(raw)
	want := []string{"./default.js"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decodePackageImportTargets = %v, want %v", got, want)
	}
}

func TestDecodePackageImportTargets_ArrayFallbacks(t *testing.T) {
	raw := json.RawMessage(`[
  null,
  42,
  {"browser": "./browser.js", "default": "./default.js"},
  "./last.js"
]`)
	got := decodePackageImportTargets(raw)
	want := []string{"./default.js", "./last.js"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decodePackageImportTargets = %v, want %v", got, want)
	}
}

func TestDeduplicateAndSort(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{}, nil},
		{[]string{"b", "a", "b", "c"}, []string{"a", "b", "c"}},
		{[]string{"x"}, []string{"x"}},
	}
	for _, c := range cases {
		got := deduplicateAndSort(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("deduplicateAndSort(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func ruleDirective(key, value string) rule.Directive {
	return rule.Directive{Key: key, Value: value}
}

type staticImportResolver struct {
	importsByName map[string][]gazelleresolve.ImportSpec
}

func (r staticImportResolver) Name() string { return languageName }

func (r staticImportResolver) Imports(c *config.Config, rl *rule.Rule, f *rule.File) []gazelleresolve.ImportSpec {
	return r.importsByName[rl.Name()]
}

func (r staticImportResolver) Embeds(rl *rule.Rule, from label.Label) []label.Label {
	return nil
}

func (r staticImportResolver) Resolve(
	c *config.Config,
	ix *gazelleresolve.RuleIndex,
	rc *repo.RemoteCache,
	rl *rule.Rule,
	imports interface{},
	from label.Label,
) {
}

func TestNodeBuiltinsCovered(t *testing.T) {
	// Spot-check a few common ones to ensure the table didn't drift.
	for _, mod := range []string{"fs", "path", "crypto", "child_process", "events"} {
		if !nodeBuiltinModules[mod] {
			t.Errorf("expected %q in nodeBuiltinModules", mod)
		}
	}
}
