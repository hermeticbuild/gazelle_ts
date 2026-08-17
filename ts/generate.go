package ts

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
	"github.com/bazelbuild/buildtools/build"
	"github.com/bmatcuk/doublestar/v4"
)

// kindMatches returns true when ruleKind matches the canonical name, accounting
// for `# gazelle:map_kind` rewrites: a rule on disk may carry the post-mapped
// kind name even when our plugin emits and reasons about the canonical one.
// Without this check we'd skip user-mapped binary rules.
func kindMatches(c *config.Config, ruleKind, canonical string) bool {
	if ruleKind == canonical {
		return true
	}
	if c == nil {
		return false
	}
	if mapped, ok := c.KindMap[canonical]; ok && mapped.KindName == ruleKind {
		return true
	}
	return false
}

// ImportData carries parsed imports from GenerateRules to Resolve. Gazelle
// runs GenerateRules during the directory walk (before the RuleIndex is
// complete) and Resolve afterwards, so we stash everything we'll need here.
type ImportData struct {
	Imports     []ImportStatement // source-file imports
	TestImports []ImportStatement // test-file imports
	Globals     []GlobalReference // source-file global references
	TestGlobals []GlobalReference // test-file global references
}

const (
	binaryLibraryKey = "gazelle_ts_binary_library"
	binaryLibraryTag = "gazelle_ts_binary_library"
)

// GenerateRules walks a directory's files, partitions them into source, test,
// visual-library, and tooling-config roles, parses imports via the Rust
// subprocess, and emits rules. The merge engine reconciles the result with
// the existing BUILD content using KindInfo from kinds.go.
func (l *tsLang) GenerateRules(args language.GenerateArgs) language.GenerateResult {
	cfg, ok := args.Config.Exts[languageName].(*tsConfig)
	if !ok || !cfg.enabled {
		return language.GenerateResult{}
	}

	libName, testName, visualLibraryName := resolveRuleNames(cfg, args.Rel)
	parts := collectSrcs(args.RegularFiles, cfg)
	ownedSources := existingRuleOwnedSources(
		args.Config,
		args.File,
		cfg,
		args.RegularFiles,
		libName,
		testName,
		visualLibraryName,
	)
	parts.removeOwned(ownedSources)

	// Hand-written binaries stay thin, like Gazelle's go_binary: each binary's
	// sources and imports belong to a private generated library, while the
	// launcher consumes that library. We never generate the launcher itself.
	type binaryRef struct {
		kind  string
		name  string
		files []string
	}
	var binaries []binaryRef
	binarySources := map[string]bool{}
	if args.File != nil {
		available := make(map[string]bool, len(args.RegularFiles))
		for _, f := range args.RegularFiles {
			available[filepath.ToSlash(f)] = true
		}
		for _, r := range args.File.Rules {
			canonical := ""
			for _, k := range managedBinaryKinds {
				if kindMatches(args.Config, r.Kind(), k) {
					canonical = k
					break
				}
			}
			if canonical == "" {
				continue
			}
			ref := binaryRef{kind: canonical, name: r.Name()}
			candidates := append(
				localSourcesFromAttr(r, "entry_point", available),
				localSourcesFromAttr(r, "srcs", available)...,
			)
			seen := map[string]bool{}
			found := false
			for _, c := range candidates {
				if isTypeScriptFile(c, cfg) && !seen[c] {
					seen[c] = true
					binarySources[c] = true
					ref.files = append(ref.files, c)
					found = true
				}
			}
			if found {
				sort.Strings(ref.files)
				binaries = append(binaries, ref)
			}
		}
	}
	libSrcs := parts.lib
	testSrcs := parts.test
	visualLibrarySrcs := parts.visualLibrary

	var tsFiles []string
	for _, f := range args.RegularFiles {
		f = filepath.ToSlash(f)
		if !isTypeScriptFile(f, cfg) || (ownedSources[f] && !binarySources[f]) {
			continue
		}
		tsFiles = append(tsFiles, filepath.Join(args.Dir, f))
	}

	var sourceImports, testImports []ImportStatement
	var visualLibraryImports []ImportStatement
	var sourceGlobals, testGlobals []GlobalReference
	var visualLibraryGlobals []GlobalReference
	bundlerImportsBySpec := map[int][]ImportStatement{}
	bundlerGlobalsBySpec := map[int][]GlobalReference{}
	allRefs := map[string]ExtractedReferences{}
	if len(tsFiles) > 0 {
		allRefs, _ = l.extractImportsBatch(tsFiles)
		for _, f := range args.RegularFiles {
			if !isTypeScriptFile(f, cfg) {
				continue
			}
			if ownedSources[filepath.ToSlash(f)] {
				continue
			}
			fullPath := filepath.Join(args.Dir, f)
			refs := allRefs[fullPath]
			// Bundler-config classification wins over story/test
			// classification — matches collectSrcs.
			if idx, ok := matchBundlerConfigSpec(f, cfg); ok {
				bundlerImportsBySpec[idx] = append(bundlerImportsBySpec[idx], refs.Imports...)
				bundlerGlobalsBySpec[idx] = append(bundlerGlobalsBySpec[idx], refs.Globals...)
				continue
			}
			if isVisualLibraryFile(f, cfg) {
				visualLibraryImports = append(visualLibraryImports, refs.Imports...)
				visualLibraryGlobals = append(visualLibraryGlobals, refs.Globals...)
				continue
			}
			if isTestFile(f, cfg) {
				testImports = append(testImports, refs.Imports...)
				testGlobals = append(testGlobals, refs.Globals...)
			} else {
				sourceImports = append(sourceImports, refs.Imports...)
				sourceGlobals = append(sourceGlobals, refs.Globals...)
			}
		}
	}

	if len(libSrcs) == 0 && len(testSrcs) == 0 && len(visualLibrarySrcs) == 0 && len(binaries) == 0 && len(parts.bundlerConfigs) == 0 {
		return language.GenerateResult{}
	}

	var genRules []*rule.Rule
	var genImports []interface{}

	if len(libSrcs) > 0 {
		// Emit the abstract `ts_library` kind. Compilation-mode flags
		// (composite, declaration, source_map, transpiler, tsconfig) are
		// the wrapper macro's job — gazelle deliberately stays out of
		// that decision so consumers can swap rules_ts for any equivalent
		// rule set without rewiring the directive surface. The wrapper is
		// reached via `# gazelle:map_kind ts_library <macro> <load_path>`.
		r := rule.NewRule(KindTsLibrary, libName)
		r.SetAttr("srcs", libSrcs)
		if len(cfg.visibility) > 0 {
			r.SetAttr("visibility", cfg.visibility)
		}
		genRules = append(genRules, r)
		genImports = append(genImports, ImportData{
			Imports: sourceImports,
			Globals: sourceGlobals,
		})
	}

	if len(testSrcs) > 0 {
		// Emit the abstract ts_test kind. Test entrypoints, compile-time deps,
		// and runtime fixtures are distinct attrs so wrapper macros can
		// typecheck only the test files and consume implementation via deps.
		r := rule.NewRule(KindTsTest, testName)
		r.SetAttr("srcs", testSrcs)
		if len(cfg.testData) > 0 {
			r.SetAttr("data", cfg.testData)
		}
		if len(libSrcs) > 0 {
			r.SetAttr("deps", []string{":" + libName})
		}
		genRules = append(genRules, r)
		genImports = append(genImports, ImportData{
			TestImports: testImports,
			TestGlobals: testGlobals,
		})
	}

	if len(visualLibrarySrcs) > 0 {
		// Emit the abstract ts_visual_library kind. Visual libraries typecheck
		// separately from the package library so Storybook-only imports don't
		// leak into the library closure, while deps still include the sibling lib.
		r := rule.NewRule(KindTsVisualLibrary, visualLibraryName)
		r.SetAttr("srcs", visualLibrarySrcs)
		if len(cfg.visibility) > 0 {
			r.SetAttr("visibility", cfg.visibility)
		}
		if len(libSrcs) > 0 {
			r.SetAttr("deps", []string{":" + libName})
		}
		genRules = append(genRules, r)
		genImports = append(genImports, ImportData{
			Imports: visualLibraryImports,
			Globals: visualLibraryGlobals,
		})
	}

	// Existing binary rules consume a private library containing exactly their
	// sources. This mirrors Gazelle Go's private go_library + go_binary shape
	// without creating package-wide cycles between multiple binaries.
	for _, b := range binaries {
		libraryName := binaryLibraryName(b.name, libName)
		libraryFiles := append([]string{}, b.files...)
		seenFiles := make(map[string]bool, len(libraryFiles))
		for _, f := range libraryFiles {
			seenFiles[f] = true
		}
		for _, f := range libSrcs {
			if isDeclarationFile(f) && !seenFiles[f] {
				libraryFiles = append(libraryFiles, f)
				seenFiles[f] = true
			}
		}
		sort.Strings(libraryFiles)
		var imports []ImportStatement
		var globals []GlobalReference
		for _, f := range libraryFiles {
			refs := allRefs[filepath.Join(args.Dir, f)]
			imports = append(imports, refs.Imports...)
			globals = append(globals, refs.Globals...)
		}

		library := rule.NewRule(KindTsLibrary, libraryName)
		library.SetAttr("srcs", libraryFiles)
		library.SetAttr("tags", []string{binaryLibraryTag})
		library.SetAttr("visibility", []string{"//visibility:private"})
		library.SetPrivateAttr(binaryLibraryKey, true)
		genRules = append(genRules, library)
		genImports = append(genImports, ImportData{Imports: imports, Globals: globals})

		r := rule.NewRule(b.kind, b.name)
		if b.kind == KindTsBinary {
			r.SetAttr("deps", []string{":" + libraryName})
		} else {
			r.SetAttr("data", []string{":" + libraryName})
		}
		genRules = append(genRules, r)
		genImports = append(genImports, ImportData{})
	}

	// Bundler-config rules — one per spec target name. Multiple specs may
	// share a name (e.g. several patterns routed to a single `bundlers`
	// target); their files and imports are merged in directive order. Each
	// emitted rule resolves its own deps closure separately from the lib.
	type bundlerGroup struct {
		name    string
		srcs    []string
		imports []ImportStatement
		globals []GlobalReference
	}
	var bundlerGroups []*bundlerGroup
	bundlerGroupsByName := map[string]*bundlerGroup{}
	for idx, spec := range cfg.bundlerConfigSpecs {
		files := parts.bundlerConfigs[idx]
		if len(files) == 0 {
			continue
		}
		g := bundlerGroupsByName[spec.Name]
		if g == nil {
			g = &bundlerGroup{name: spec.Name}
			bundlerGroupsByName[spec.Name] = g
			bundlerGroups = append(bundlerGroups, g)
		}
		g.srcs = append(g.srcs, files...)
		g.imports = append(g.imports, bundlerImportsBySpec[idx]...)
		g.globals = append(g.globals, bundlerGlobalsBySpec[idx]...)
	}
	for _, g := range bundlerGroups {
		// Files are unique across specs (longest-pattern-wins), but if multiple
		// specs share a name, sort to keep srcs deterministic.
		sort.Strings(g.srcs)
		r := rule.NewRule(KindBundlerConfig, g.name)
		r.SetAttr("srcs", g.srcs)
		if len(cfg.visibility) > 0 {
			r.SetAttr("visibility", cfg.visibility)
		}
		genRules = append(genRules, r)
		genImports = append(genImports, ImportData{
			Imports: g.imports,
			Globals: g.globals,
		})
	}

	return language.GenerateResult{
		Gen:     genRules,
		Imports: genImports,
	}
}

func binaryLibraryName(binaryName, packageLibraryName string) string {
	name := binaryName + "_lib"
	if name == packageLibraryName {
		return binaryName + "_binary_lib"
	}
	return name
}

// resolveRuleNames returns the (library, test, visual library) rule names for
// a directory, applying the directive overrides if set or falling back to
// package-name-derived defaults.
//
// Defaults — given a package at //apps/web (rel = "apps/web"):
//
//	library: "web"      → //apps/web:web (Bazel shortens to //apps/web)
//	test:    "web_test"  → //apps/web:web_test
//	visual library: "web_visual_library" → //apps/web:web_visual_library
//
// They can be overridden per-tree via the ts_library_name / ts_test_name /
// ts_visual_library_name directives. At the repo root (rel = ""), where there's no
// basename to derive from, library falls back to "lib", test to "test", and
// visual library to "visual_library".
func resolveRuleNames(cfg *tsConfig, rel string) (libName, testName, visualLibraryName string) {
	base := filepath.Base(rel)
	if base == "." || base == "" || base == "/" {
		base = ""
	}

	libName = cfg.libraryName
	if libName == "" {
		if base != "" {
			libName = base
		} else {
			libName = "lib"
		}
	}

	testName = cfg.testName
	if testName == "" {
		if base != "" {
			testName = base + "_test"
		} else {
			testName = "test"
		}
	}

	visualLibraryName = cfg.visualLibraryName
	if visualLibraryName == "" {
		if base != "" {
			visualLibraryName = base + "_visual_library"
		} else {
			visualLibraryName = "visual_library"
		}
	}
	return
}

// isTypeScriptFile checks the configured extensions list.
func isTypeScriptFile(name string, cfg *tsConfig) bool {
	for _, ext := range cfg.extensions {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

func isDeclarationFile(name string) bool {
	return strings.HasSuffix(name, ".d.ts") ||
		strings.HasSuffix(name, ".d.mts") ||
		strings.HasSuffix(name, ".d.cts")
}

// isTestFile matches the file path against any of the configured test
// patterns. Patterns may contain `**` (matches across directories) and `*`
// (matches within a path segment).
func isTestFile(name string, cfg *tsConfig) bool {
	for _, pat := range cfg.testPatterns {
		if matchPathPattern(pat, name) {
			return true
		}
	}
	return false
}

// isVisualLibraryFile matches the file path against configured visual-library
// patterns. Defaults keep `*.story.tsx` and `*.visual.tsx` out of the library
// target.
func isVisualLibraryFile(name string, cfg *tsConfig) bool {
	for _, pat := range cfg.visualLibraryPatterns {
		if matchPathPattern(pat, name) {
			return true
		}
	}
	return false
}

// matchTestPattern matches the same doublestar glob syntax used by the other
// path-pattern directives. Invalid in-progress patterns simply don't match.
func matchTestPattern(pattern, name string) bool {
	return matchPathPattern(pattern, name)
}

func matchPathPattern(pattern, name string) bool {
	ok, err := doublestar.Match(pattern, name)
	return err == nil && ok
}

// partitionedSrcs is the result of slicing a directory's TS files across the
// four roles a file can play: library source, test source, visual-library
// source, or bundler-config (one bucket per matched ts_bundler_config_pattern
// spec, keyed by spec index). Each slice is sorted for deterministic BUILD
// output.
type partitionedSrcs struct {
	lib            []string
	test           []string
	visualLibrary  []string
	bundlerConfigs map[int][]string
}

func (p *partitionedSrcs) removeOwned(owned map[string]bool) {
	p.lib = withoutOwnedSources(p.lib, owned)
	p.test = withoutOwnedSources(p.test, owned)
	p.visualLibrary = withoutOwnedSources(p.visualLibrary, owned)
	for index, sources := range p.bundlerConfigs {
		p.bundlerConfigs[index] = withoutOwnedSources(sources, owned)
	}
}

func withoutOwnedSources(sources []string, owned map[string]bool) []string {
	kept := make([]string, 0, len(sources))
	for _, source := range sources {
		if !owned[filepath.ToSlash(source)] {
			kept = append(kept, source)
		}
	}
	return kept
}

func existingRuleOwnedSources(
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
		if isGeneratedRule(c, r, cfg, libName, testName, visualLibraryName) {
			continue
		}
		for _, attr := range []string{"entry_point", "srcs"} {
			for _, source := range localSourcesFromAttr(r, attr, available) {
				if isTypeScriptFile(source, cfg) {
					owned[source] = true
				}
			}
		}
	}
	return owned
}

func isGeneratedRule(
	c *config.Config,
	r *rule.Rule,
	cfg *tsConfig,
	libName string,
	testName string,
	visualLibraryName string,
) bool {
	if kindMatches(c, r.Kind(), KindTsLibrary) && r.Name() == libName {
		return true
	}
	if kindMatches(c, r.Kind(), KindTsTest) && r.Name() == testName {
		return true
	}
	if kindMatches(c, r.Kind(), KindTsVisualLibrary) && r.Name() == visualLibraryName {
		return true
	}
	if !kindMatches(c, r.Kind(), KindBundlerConfig) {
		return false
	}
	for _, spec := range cfg.bundlerConfigSpecs {
		if spec.Name == r.Name() {
			return true
		}
	}
	return false
}

func localSourcesFromAttr(r *rule.Rule, attr string, available map[string]bool) []string {
	seen := map[string]bool{}
	var sources []string
	add := func(source string) {
		source = normalizeLocalSource(source)
		if !available[source] || seen[source] {
			return
		}
		seen[source] = true
		sources = append(sources, source)
	}

	collectLocalSources(r.Attr(attr), available, add)
	sort.Strings(sources)
	return sources
}

func collectLocalSources(expr build.Expr, available map[string]bool, add func(string)) {
	if expr == nil {
		return
	}
	if glob, ok := rule.ParseGlobExpr(expr); ok {
		for source := range available {
			if matchesSourceGlob(source, glob.Patterns, glob.Excludes) {
				add(source)
			}
		}
		return
	}

	switch expr := expr.(type) {
	case *build.StringExpr:
		add(expr.Value)
	case *build.ListExpr:
		for _, item := range expr.List {
			collectLocalSources(item, available, add)
		}
	case *build.BinaryExpr:
		if expr.Op != "+" {
			return
		}
		collectLocalSources(expr.X, available, add)
		collectLocalSources(expr.Y, available, add)
	case *build.CallExpr:
		callee, ok := expr.X.(*build.Ident)
		if !ok || callee.Name != "select" {
			return
		}
		for _, arg := range expr.List {
			collectLocalSources(arg, available, add)
		}
	case *build.DictExpr:
		for _, item := range expr.List {
			collectLocalSources(item.Value, available, add)
		}
	case *build.KeyValueExpr:
		collectLocalSources(expr.Value, available, add)
	}
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

func matchesSourceGlob(source string, patterns []string, excludes []string) bool {
	for _, exclude := range excludes {
		if matchPathPattern(exclude, source) {
			return false
		}
	}
	for _, pattern := range patterns {
		if matchPathPattern(pattern, source) {
			return true
		}
	}
	return false
}

// collectSrcs partitions the directory's files for emission. Bundler-config
// patterns take precedence over visual-library and test patterns, so a file
// matching both goes to the bundler-config bucket — the boundary the directive
// enforces is stronger than the normal source split.
func collectSrcs(regularFiles []string, cfg *tsConfig) partitionedSrcs {
	out := partitionedSrcs{bundlerConfigs: map[int][]string{}}
	for _, f := range regularFiles {
		if !isTypeScriptFile(f, cfg) {
			continue
		}
		if idx, ok := matchBundlerConfigSpec(f, cfg); ok {
			out.bundlerConfigs[idx] = append(out.bundlerConfigs[idx], f)
			continue
		}
		if isVisualLibraryFile(f, cfg) {
			out.visualLibrary = append(out.visualLibrary, f)
			continue
		}
		if isTestFile(f, cfg) {
			out.test = append(out.test, f)
			continue
		}
		out.lib = append(out.lib, f)
	}
	sort.Strings(out.lib)
	sort.Strings(out.test)
	sort.Strings(out.visualLibrary)
	for k, v := range out.bundlerConfigs {
		sort.Strings(v)
		out.bundlerConfigs[k] = v
	}
	return out
}

// matchBundlerConfigSpec returns the index of the longest-matching spec for
// the given file path (relative to the package), or -1, false. Longest
// pattern wins so a more-specific spec like `vite.config.production.ts`
// overrides a less-specific one like `vite.config.*` for the same file.
func matchBundlerConfigSpec(name string, cfg *tsConfig) (int, bool) {
	bestIdx := -1
	bestLen := -1
	for i, spec := range cfg.bundlerConfigSpecs {
		ok, err := doublestar.Match(spec.Pattern, name)
		if err != nil || !ok {
			continue
		}
		if len(spec.Pattern) > bestLen {
			bestLen = len(spec.Pattern)
			bestIdx = i
		}
	}
	return bestIdx, bestIdx >= 0
}
