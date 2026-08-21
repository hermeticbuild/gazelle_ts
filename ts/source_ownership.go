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

type sourceOwnership struct {
	claimed      map[string]bool
	compileRoots map[string]bool
	providers    map[string]bool
}

type sourcePartitionKind uint8

const (
	sourcePartitionLibrary sourcePartitionKind = iota
	sourcePartitionTest
	sourcePartitionVisual
	sourcePartitionBundler
)

type sourcePartition struct {
	kind         sourcePartitionKind
	bundlerIndex int
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
			provides := !generated && attr == "srcs" && sourceRuleProvidesImports(c, r)
			sources, _ := localSourcesFromAttr(r, attr, available)
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
	if r.Kind() == "filegroup" {
		return false
	}
	return configuredSourceProviderKind(c, r.Kind()) || r.Kind() == "js_test"
}

func sourceRuleProvidesImports(c *config.Config, r *rule.Rule) bool {
	if kindMatches(c, r.Kind(), KindTsLibrary) {
		return true
	}
	return r.Attr("srcs") != nil && configuredSourceProviderKind(c, r.Kind())
}

func configuredSourceProviderKind(c *config.Config, kind string) bool {
	if c == nil {
		return false
	}
	cfg, ok := c.Exts[languageName].(*tsConfig)
	return ok && cfg.sourceProviderKinds[kind]
}

func (l *tsLang) ownedSourcePlacement(
	c *config.Config,
	dir string,
	rel string,
	regularFiles []string,
	parts partitionedSrcs,
	ownership sourceOwnership,
	allRefs map[string]ExtractedReferences,
	libraryBackedBinaryFiles map[string]bool,
) (map[string]bool, map[string]sourcePartition) {
	assigned := map[string]sourcePartition{}
	queue := make([]string, 0, len(regularFiles))
	assign := func(source string, destination sourcePartition) {
		source = filepath.ToSlash(source)
		current, ok := assigned[source]
		if ok {
			destination = mergeSourcePartitions(current, destination)
			if destination == current {
				return
			}
		}
		assigned[source] = destination
		queue = append(queue, source)
	}

	for source, destination := range parts.sourcePartitions() {
		if ownership.claimed[source] {
			continue
		}
		assign(source, destination)
	}
	for source := range libraryBackedBinaryFiles {
		if ownership.providers[source] || ownership.compileRoots[source] {
			continue
		}
		assign(source, sourcePartition{kind: sourcePartitionLibrary})
	}
	index := l.newLocalSourceIndex(c, rel, regularFiles, ownership.claimed)
	for queueIndex := 0; queueIndex < len(queue); queueIndex++ {
		source := queue[queueIndex]
		destination := assigned[source]
		refs := allRefs[filepath.Join(dir, filepath.FromSlash(source))]
		for _, imp := range refs.Imports {
			imported, ok := l.localImportedSource(c, index, imp)
			if !ok || ownership.providers[imported] || ownership.compileRoots[imported] {
				continue
			}
			assign(imported, destination)
		}
	}

	removable := map[string]bool{}
	for source := range ownership.claimed {
		if ownership.providers[source] || ownership.compileRoots[source] {
			removable[source] = true
			delete(assigned, source)
			continue
		}
		if _, required := assigned[source]; !required {
			removable[source] = true
		}
	}

	placements := map[string]sourcePartition{}
	for source, destination := range assigned {
		if ownership.claimed[source] {
			placements[source] = destination
		}
	}
	return removable, placements
}

func mergeSourcePartitions(left, right sourcePartition) sourcePartition {
	if left == right {
		return left
	}
	return sourcePartition{kind: sourcePartitionLibrary}
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

func localSourcesFromAttr(r *rule.Rule, attr string, available map[string]bool) ([]string, bool) {
	var candidates []string
	complete := true
	if attr == "entry_point" {
		entryPoint, ok := literalStringAttr(r, attr)
		if !ok {
			return nil, false
		}
		if entryPoint != "" {
			candidates = []string{entryPoint}
		}
	} else {
		candidates, complete = literalStringListAttr(r, attr)
	}

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

func hasOpaqueManagedBinarySources(c *config.Config, file *rule.File) bool {
	if file == nil {
		return false
	}
	for _, r := range file.Rules {
		if !isManagedBinary(c, r) {
			continue
		}
		if r.Attr("entry_point") != nil {
			if _, literal := literalStringAttr(r, "entry_point"); !literal {
				return true
			}
		}
		if r.Attr("srcs") != nil {
			if _, complete := literalStringListAttr(r, "srcs"); !complete {
				return true
			}
		}
	}
	return false
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
