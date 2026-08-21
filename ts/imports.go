package ts

import (
	"os"
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
//
// Test rules don't export reusable modules, so they don't appear in the index.
func (l *tsLang) Imports(c *config.Config, r *rule.Rule, f *rule.File) []resolve.ImportSpec {
	if !kindMatches(c, r.Kind(), KindTsLibrary) {
		return nil
	}

	pkg := f.Pkg
	specs := []resolve.ImportSpec{
		{Lang: languageName, Imp: pkg},
		{Lang: languageName, Imp: pkg + "/*"},
	}
	srcs, _ := literalStringListAttr(r, "srcs")
	specs = append(specs, importSpecsForLiteralSrcs(c, f, pkg, srcs)...)
	return specs
}

func importSpecsForLiteralSrcs(c *config.Config, file *rule.File, pkg string, srcs []string) []resolve.ImportSpec {
	exact := packageLiteralImportPaths(c, file, pkg)
	seen := make(map[string]bool, len(srcs)*2)
	imports := make([]string, 0, len(srcs)*2)
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
	for _, importPath := range append([]string(nil), imports...) {
		if !strings.HasSuffix(importPath, "/index") {
			continue
		}
		alias := strings.TrimSuffix(importPath, "/index")
		if alias == "" || seen[alias] || exact[alias] || packageSourceExistsForImportPath(c, pkg, alias) {
			continue
		}
		seen[alias] = true
		imports = append(imports, alias)
	}
	sort.Strings(imports)

	specs := make([]resolve.ImportSpec, 0, len(imports))
	for _, imp := range imports {
		specs = append(specs, resolve.ImportSpec{Lang: languageName, Imp: imp})
	}
	return specs
}

func packageLiteralImportPaths(c *config.Config, file *rule.File, pkg string) map[string]bool {
	paths := map[string]bool{}
	if file == nil {
		return paths
	}
	for _, r := range file.Rules {
		if !kindMatches(c, r.Kind(), KindTsLibrary) {
			continue
		}
		srcs, _ := literalStringListAttr(r, "srcs")
		for _, src := range srcs {
			if importPath, ok := importPathForSrc(c, pkg, src); ok {
				paths[importPath] = true
			}
		}
	}
	return paths
}

func packageSourceExistsForImportPath(c *config.Config, pkg, importPath string) bool {
	if c == nil || c.RepoRoot == "" {
		return false
	}
	relative := importPath
	if pkg != "" {
		prefix := pkg + "/"
		if !strings.HasPrefix(importPath, prefix) {
			return false
		}
		relative = strings.TrimPrefix(importPath, prefix)
	}
	for _, extension := range importSpecSourceExtensions(c) {
		candidate := filepath.Join(c.RepoRoot, filepath.FromSlash(pkg), filepath.FromSlash(relative+extension))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func importPathForSrc(c *config.Config, pkg, src string) (string, bool) {
	if isNonSourceSrc(src) {
		return "", false
	}
	cleanSrc := normalizeLocalSource(src)
	if cleanSrc == "" {
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
