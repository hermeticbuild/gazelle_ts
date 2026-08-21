package ts

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

type sourceOwnership struct {
	claimed      map[string]bool
	compileRoots map[string]bool
	providers    map[string]bool
}

// existingRuleSourceOwnership classifies TypeScript-shaped files referenced
// by hand-written rules. Compile providers stay separate from generated
// targets, while resource-only ownership may be overridden when generated
// source actually imports the file.
func existingRuleSourceOwnership(
	c *config.Config,
	file *rule.File,
	cfg *tsConfig,
	regularFiles []string,
	libName string,
	testName string,
	visualLibraryName string,
) sourceOwnership {
	ownership := sourceOwnership{
		claimed:      map[string]bool{},
		compileRoots: map[string]bool{},
		providers:    map[string]bool{},
	}
	if file == nil {
		return ownership
	}

	available := make(map[string]bool, len(regularFiles))
	for _, source := range regularFiles {
		available[filepath.ToSlash(source)] = true
	}

	for _, r := range file.Rules {
		if isManagedBinary(c, r) {
			continue
		}

		attrs := []string{"entry_point", "srcs"}
		generated := isGeneratedRule(c, r, cfg, libName, testName, visualLibraryName)
		if generated {
			attrs = nil
			// Explicit data on a generated library may intentionally keep a
			// TypeScript-shaped runtime resource out of compilation. Test data is
			// generated from ts_test_data and must remain replaceable.
			if !kindMatches(c, r.Kind(), KindTsTest) {
				attrs = []string{"data"}
			}
		}

		for _, attr := range attrs {
			compiles := !generated && sourceAttrProvidesCompilation(c, r, attr)
			provides := !generated && attr == "srcs" && kindMatches(c, r.Kind(), KindTsLibrary)
			sources := localSourcesFromAttr(r, attr, available)
			for _, source := range sources {
				if isTypeScriptFile(source, cfg) {
					ownership.claimed[source] = true
					if compiles {
						ownership.compileRoots[source] = true
					}
					if provides {
						ownership.providers[source] = true
					}
				}
			}
		}
	}
	return ownership
}

func sourceAttrProvidesCompilation(c *config.Config, r *rule.Rule, attr string) bool {
	if attr == "entry_point" {
		return true
	}
	if attr != "srcs" {
		return false
	}
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
	return r.Kind() == "js_test"
}

func (l *tsLang) removableOwnedSources(
	c *config.Config,
	dir string,
	rel string,
	regularFiles []string,
	ownership sourceOwnership,
	allRefs map[string]ExtractedReferences,
	libraryBackedBinaryFiles map[string]bool,
) map[string]bool {
	required := map[string]bool{}
	queued := map[string]bool{}
	queue := make([]string, 0, len(regularFiles))
	enqueue := func(source string) {
		source = filepath.ToSlash(source)
		if queued[source] {
			return
		}
		queued[source] = true
		queue = append(queue, source)
	}

	for _, source := range regularFiles {
		source = filepath.ToSlash(source)
		if !ownership.claimed[source] || (ownership.compileRoots[source] && !ownership.providers[source]) {
			enqueue(source)
		}
	}
	for source := range libraryBackedBinaryFiles {
		if ownership.providers[source] {
			continue
		}
		if ownership.claimed[source] {
			required[source] = true
		}
		enqueue(source)
	}
	index := l.newLocalSourceIndex(c, rel, regularFiles, ownership.claimed)
	for queueIndex := 0; queueIndex < len(queue); queueIndex++ {
		source := queue[queueIndex]
		refs := allRefs[filepath.Join(dir, filepath.FromSlash(source))]
		for _, imp := range refs.Imports {
			imported, ok := l.localImportedSource(c, index, imp)
			if !ok || ownership.providers[imported] {
				continue
			}
			if ownership.claimed[imported] {
				required[imported] = true
			}
			enqueue(imported)
		}
	}

	removable := map[string]bool{}
	for source := range ownership.claimed {
		if ownership.providers[source] || !required[source] {
			removable[source] = true
		}
	}
	return removable
}

type localSourceIndex struct {
	byImportPath    map[string]string
	subpathPatterns []string
}

func (l *tsLang) newLocalSourceIndex(
	c *config.Config,
	rel string,
	regularFiles []string,
	candidates map[string]bool,
) localSourceIndex {
	index := localSourceIndex{byImportPath: map[string]string{}}
	for _, source := range regularFiles {
		source = filepath.ToSlash(source)
		if !candidates[source] {
			continue
		}
		workspacePath := filepath.ToSlash(filepath.Join(rel, source))
		withoutExtension := trimImportPathExtension(c, workspacePath)
		index.byImportPath[withoutExtension] = source
	}
	for _, source := range regularFiles {
		source = filepath.ToSlash(source)
		if !candidates[source] {
			continue
		}
		workspacePath := filepath.ToSlash(filepath.Join(rel, source))
		withoutExtension := trimImportPathExtension(c, workspacePath)
		if filepath.Base(withoutExtension) != "index" {
			continue
		}
		alias := filepath.ToSlash(filepath.Dir(withoutExtension))
		if _, exactFileExists := index.byImportPath[alias]; !exactFileExists {
			index.byImportPath[alias] = source
		}
	}
	for pattern := range l.subpathImportsMap {
		index.subpathPatterns = append(index.subpathPatterns, pattern)
	}
	sort.Slice(index.subpathPatterns, func(i, j int) bool {
		if len(index.subpathPatterns[i]) == len(index.subpathPatterns[j]) {
			return index.subpathPatterns[i] < index.subpathPatterns[j]
		}
		return len(index.subpathPatterns[i]) > len(index.subpathPatterns[j])
	})
	return index
}

func (l *tsLang) localImportedSource(c *config.Config, index localSourceIndex, imp ImportStatement) (string, bool) {
	lookup := func(importPath string) (string, bool) {
		importPath = filepath.ToSlash(filepath.Clean(importPath))
		importPath = strings.TrimPrefix(importPath, "./")
		source, ok := index.byImportPath[trimImportPathExtension(c, importPath)]
		return source, ok
	}

	if strings.HasPrefix(imp.ImportPath, ".") {
		sourceFile := imp.SourceFile
		if filepath.IsAbs(sourceFile) {
			if relative, err := filepath.Rel(c.RepoRoot, sourceFile); err == nil {
				sourceFile = relative
			}
		}
		return lookup(filepath.Join(filepath.Dir(sourceFile), imp.ImportPath))
	}

	if source, ok := lookup(imp.ImportPath); ok {
		return source, true
	}
	for _, pattern := range index.subpathPatterns {
		capture, ok := matchSubpathImportPattern(pattern, imp.ImportPath)
		if !ok {
			continue
		}
		for _, target := range l.subpathImportsMap[pattern] {
			if strings.HasPrefix(target, "//") || strings.HasPrefix(target, "@") {
				continue
			}
			if source, ok := lookup(strings.ReplaceAll(target, "*", capture)); ok {
				return source, true
			}
		}
	}
	return "", false
}

func localSourcesFromAttr(r *rule.Rule, attr string, available map[string]bool) []string {
	seen := map[string]bool{}
	var sources []string
	candidates := append([]string{r.AttrString(attr)}, r.AttrStrings(attr)...)
	for _, source := range candidates {
		source = normalizeLocalSource(source)
		if !available[source] || seen[source] {
			continue
		}
		seen[source] = true
		sources = append(sources, source)
	}
	sort.Strings(sources)
	return sources
}

func normalizeLocalSource(source string) string {
	source = filepath.ToSlash(source)
	if strings.HasPrefix(source, "//") || strings.HasPrefix(source, "@") {
		return ""
	}
	source = strings.TrimPrefix(source, ":")
	source = strings.TrimPrefix(source, "./")
	return source
}
