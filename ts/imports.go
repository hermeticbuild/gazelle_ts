package ts

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/resolve"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// Imports returns the import specs that a rule provides; gazelle stores these
// in a reverse index that maps import paths to Bazel labels.
//
// For a library at //packages/foo, we register:
//   - "packages/foo"   (exact match)
//   - "packages/foo/*" (wildcard for subpath imports)
//   - "packages/foo/bar" for a literal src "bar.ts"
//
// This lets Resolve() answer queries like
// `#packages/foo/bar.js` → //packages/foo and `packages/foo` → //packages/foo.
// Source-level specs let hand-written split targets own files within the same
// package without falling back to a package aggregate.
// Binary-private libraries publish only source-level specs so they don't
// compete with the package library for broad package imports.
//
// Test rules don't export reusable modules, so they don't appear in the index.
func (l *tsLang) Imports(c *config.Config, r *rule.Rule, f *rule.File) []resolve.ImportSpec {
	if !kindMatches(c, r.Kind(), KindTsLibrary) {
		return nil
	}

	pkg := f.Pkg
	var specs []resolve.ImportSpec
	if private, _ := r.PrivateAttr(binaryLibraryKey).(bool); !private {
		specs = append(specs,
			resolve.ImportSpec{Lang: languageName, Imp: pkg},
			resolve.ImportSpec{Lang: languageName, Imp: pkg + "/*"},
		)
	}
	specs = append(specs, importSpecsForLiteralSrcs(c, pkg, r.AttrStrings("srcs"))...)
	return specs
}

func importSpecsForLiteralSrcs(c *config.Config, pkg string, srcs []string) []resolve.ImportSpec {
	seen := make(map[string]bool, len(srcs))
	imports := make([]string, 0, len(srcs))
	for _, src := range srcs {
		if isNonSourceSrc(src) {
			continue
		}
		importPath, ok := importPathForSrc(c, pkg, src)
		if !ok || seen[importPath] {
			continue
		}
		seen[importPath] = true
		imports = append(imports, importPath)
	}
	sort.Strings(imports)

	specs := make([]resolve.ImportSpec, 0, len(imports))
	for _, imp := range imports {
		specs = append(specs, resolve.ImportSpec{Lang: languageName, Imp: imp})
	}
	return specs
}

func importPathForSrc(c *config.Config, pkg, src string) (string, bool) {
	cleanSrc := filepath.ToSlash(filepath.Clean(src))
	cleanSrc = strings.TrimPrefix(cleanSrc, "./")
	cleanSrc = strings.TrimPrefix(cleanSrc, ":")
	if cleanSrc == "" || cleanSrc == "." || strings.HasPrefix(cleanSrc, "../") {
		return "", false
	}
	for _, ext := range importSpecSourceExtensions(c) {
		if strings.HasSuffix(cleanSrc, ext) {
			withoutExt := strings.TrimSuffix(cleanSrc, ext)
			if pkg == "" {
				return withoutExt, true
			}
			return pkg + "/" + withoutExt, true
		}
	}
	return "", false
}

func isNonSourceSrc(src string) bool {
	return src == "" ||
		strings.HasPrefix(src, "//") ||
		strings.HasPrefix(src, "@") ||
		strings.ContainsAny(src, "*?[")
}

func importSpecSourceExtensions(c *config.Config) []string {
	return sortedUniqueExtensions(append(configuredSourceExtensions(c), ".js", ".jsx", ".mjs", ".cjs"))
}

func importPathExtensions(c *config.Config) []string {
	return sortedUniqueExtensions(append(configuredSourceExtensions(c), ".js", ".jsx", ".mjs", ".cjs"))
}

func configuredSourceExtensions(c *config.Config) []string {
	if c == nil {
		return append([]string(nil), defaultExtensions...)
	}
	cfg, ok := c.Exts[languageName].(*tsConfig)
	if !ok || len(cfg.extensions) == 0 {
		return append([]string(nil), defaultExtensions...)
	}
	return append([]string(nil), cfg.extensions...)
}

func sortedUniqueExtensions(exts []string) []string {
	seen := make(map[string]bool, len(exts))
	unique := make([]string, 0, len(exts))
	for _, ext := range exts {
		if ext == "" || seen[ext] {
			continue
		}
		seen[ext] = true
		unique = append(unique, ext)
	}
	sort.Slice(unique, func(i, j int) bool {
		if len(unique[i]) == len(unique[j]) {
			return unique[i] < unique[j]
		}
		return len(unique[i]) > len(unique[j])
	})
	return unique
}

func trimImportPathExtension(c *config.Config, path string) string {
	for _, ext := range importPathExtensions(c) {
		if strings.HasSuffix(path, ext) {
			return strings.TrimSuffix(path, ext)
		}
	}
	return path
}
