package ts

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
	bzl "github.com/bazelbuild/buildtools/build"
)

// existingCompilationOwnedSources returns package-local TypeScript files
// compiled by hand-written TypeScript rules. Resource and runtime rules do not
// own compilation, so their files remain available to generated rules.
func existingCompilationOwnedSources(
	c *config.Config,
	file *rule.File,
	cfg *tsConfig,
	regularFiles []string,
	libName string,
	testName string,
	visualLibraryName string,
) map[string]bool {
	owned := map[string]bool{}
	if file == nil {
		return owned
	}

	available := make(map[string]bool, len(regularFiles))
	for _, source := range regularFiles {
		available[filepath.ToSlash(source)] = true
	}

	for _, r := range file.Rules {
		if !isCompilationRule(c, r) || isGeneratedRule(c, r, cfg, libName, testName, visualLibraryName) {
			continue
		}
		sources, _ := localSourcesFromListAttr(r, "srcs", available)
		for _, source := range sources {
			if isTypeScriptFile(source, cfg) {
				owned[source] = true
			}
		}
	}
	return owned
}

func isCompilationRule(c *config.Config, r *rule.Rule) bool {
	for _, kind := range []string{
		KindTsLibrary,
		KindTsTest,
		KindTsVisualLibrary,
		KindBundlerConfig,
	} {
		if kindMatches(c, r.Kind(), kind) {
			return true
		}
	}
	return false
}

func localSourcesFromListAttr(r *rule.Rule, attr string, available map[string]bool) ([]string, bool) {
	candidates, complete := literalStringListAttr(r, attr)
	seen := map[string]bool{}
	var sources []string
	for _, source := range candidates {
		source = normalizeLocalSource(source)
		if !available[source] || seen[source] {
			continue
		}
		seen[source] = true
		sources = append(sources, source)
	}
	sort.Strings(sources)
	return sources, complete
}

// literalStringListAttr returns direct string elements and reports whether
// the attribute is absent or consists entirely of strings. It never evaluates
// nested expressions such as glob, select, concatenation, or identifiers.
func literalStringListAttr(r *rule.Rule, name string) ([]string, bool) {
	expr := r.Attr(name)
	if expr == nil {
		return nil, true
	}
	list, ok := expr.(*bzl.ListExpr)
	if !ok {
		return nil, false
	}
	values := make([]string, len(list.List))
	complete := true
	n := 0
	for _, item := range list.List {
		value, ok := item.(*bzl.StringExpr)
		if !ok {
			complete = false
			continue
		}
		values[n] = value.Value
		n++
	}
	return values[:n], complete
}

// literalStringAttr reports absent attributes as complete and rejects every
// non-string expression without evaluating it.
func literalStringAttr(r *rule.Rule, name string) (string, bool) {
	expr := r.Attr(name)
	if expr == nil {
		return "", true
	}
	value, ok := expr.(*bzl.StringExpr)
	if !ok {
		return "", false
	}
	return value.Value, true
}

func normalizeLocalSource(source string) string {
	source = filepath.ToSlash(source)
	if source == "" || strings.HasPrefix(source, "//") || strings.HasPrefix(source, "@") {
		return ""
	}
	source = strings.TrimPrefix(source, ":")
	for strings.HasPrefix(source, "./") {
		source = strings.TrimPrefix(source, "./")
	}
	if source == "" || strings.HasPrefix(source, "/") || strings.Contains(source, ":") {
		return ""
	}
	for _, segment := range strings.Split(source, "/") {
		if segment == ".." {
			return ""
		}
	}
	source = path.Clean(source)
	if source == "." {
		return ""
	}
	return source
}
