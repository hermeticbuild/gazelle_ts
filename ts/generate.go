package ts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
	"github.com/bmatcuk/doublestar/v4"
)

const commandLibrarySuffix = "_lib"

// kindMatches returns true when ruleKind matches the canonical name, accounting
// for `# gazelle:map_kind` rewrites: a rule on disk may carry the post-mapped
// kind name even when our plugin emits and reasons about the canonical one.
// Without this check we'd skip user-mapped js_binary rules and stop
// auto-managing their `data` attr.
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
	Imports           []ImportStatement // source-file imports
	TestImports       []ImportStatement // test-file imports
	Globals           []GlobalReference // source-file global references
	TestGlobals       []GlobalReference // test-file global references
	BinaryUsesLibrary bool
}

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
	libName = existingLibraryName(args.Config, args.File, cfg, libName)
	parts := collectSrcs(args.RegularFiles, cfg)
	availableSources := make(map[string]bool, len(args.RegularFiles))
	for _, source := range args.RegularFiles {
		availableSources[filepath.ToSlash(source)] = true
	}
	sourceExists := func(source string) bool {
		if availableSources[source] {
			return true
		}
		info, err := os.Stat(filepath.Join(args.Dir, filepath.FromSlash(source)))
		return err == nil && !info.IsDir()
	}
	ownedSources := existingCompilationOwnedSources(
		args.Config,
		args.File,
		cfg,
		args.RegularFiles,
		libName,
		testName,
		visualLibraryName,
	)
	parts.removeOwned(ownedSources)
	libSrcs := parts.lib
	testSrcs := parts.test
	visualLibrarySrcs := parts.visualLibrary
	librarySources := make(map[string]bool, len(libSrcs))
	for _, source := range libSrcs {
		librarySources[filepath.ToSlash(source)] = true
	}

	var tsFiles []string
	for _, f := range args.RegularFiles {
		if isTypeScriptFile(f, cfg) && !ownedSources[filepath.ToSlash(f)] {
			tsFiles = append(tsFiles, filepath.Join(args.Dir, f))
		}
	}

	// Discover hand-written binaries. Oxc-detected main
	// entrypoints are added after extraction unless an existing binary already
	// claims the source.
	type binaryRef struct {
		kind       string
		name       string
		existing   bool
		files      []string // package-relative TS files whose imports need direct resolution
		entryPoint string
		srcs       []string
		deps       []string
	}
	var binaries []binaryRef
	var emptyRules []*rule.Rule
	binaryFiles := map[string]bool{}
	existingNames := map[string]bool{}
	reservedBinaries := map[string]string{}
	if args.File != nil {
		seen := make(map[string]bool, len(tsFiles))
		for _, f := range tsFiles {
			seen[f] = true
		}
		for _, r := range args.File.Rules {
			existingNames[r.Name()] = true
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
			ref := binaryRef{
				kind:     canonical,
				name:     r.Name(),
				existing: true,
			}
			entryPoint, entryPointIsLiteral := literalStringAttr(r, "entry_point")
			srcs, srcsAreLiteral := literalStringListAttr(r, "srcs")
			if !entryPointIsLiteral || !srcsAreLiteral {
				knownSources := append([]string(nil), srcs...)
				if entryPointIsLiteral {
					knownSources = append(knownSources, entryPoint)
				}
				for _, source := range knownSources {
					source = normalizeLocalSource(source)
					if sourceExists(source) && isTypeScriptFile(source, cfg) {
						binaryFiles[source] = true
					}
				}
				continue
			}
			ref.entryPoint = normalizeLocalSource(entryPoint)
			for _, source := range srcs {
				source = normalizeLocalSource(source)
				if sourceExists(source) && isTypeScriptFile(source, cfg) {
					ref.srcs = append(ref.srcs, source)
				}
			}
			if canonical == KindTsBinary && binaryEntryPointInLibrary(ref.entryPoint, ref.srcs, librarySources) {
				ref.deps = []string{":" + libName}
			}
			candidates := append([]string{ref.entryPoint}, ref.srcs...)
			refFiles := map[string]bool{}
			for _, c := range candidates {
				if !sourceExists(c) || !isTypeScriptFile(c, cfg) || refFiles[c] {
					continue
				}
				refFiles[c] = true
				ref.files = append(ref.files, c)
				full := filepath.Join(args.Dir, c)
				if !seen[full] {
					tsFiles = append(tsFiles, full)
					seen[full] = true
				}
			}
			if len(ref.files) > 0 {
				if canonical == KindTsBinary && len(ref.files) == 1 &&
					r.Name() == binaryNameForSource(ref.files[0], cfg) {
					// This name is reserved for generated main entrypoints. Re-detect
					// it so removing or changing main removes the stale binary rule.
					delete(existingNames, r.Name())
					reservedBinaries[filepath.ToSlash(ref.files[0])] = r.Name()
					continue
				}
				for _, file := range ref.files {
					file = filepath.ToSlash(file)
					binaryFiles[file] = true
				}
				binaries = append(binaries, ref)
			}
		}
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
			if !isTypeScriptFile(f, cfg) || ownedSources[filepath.ToSlash(f)] {
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
	detectedBinaries := map[string]bool{}
	reservedSources := make([]string, 0, len(reservedBinaries))
	for source := range reservedBinaries {
		reservedSources = append(reservedSources, source)
	}
	sort.Strings(reservedSources)
	for _, source := range reservedSources {
		name := reservedBinaries[source]
		if !allRefs[filepath.Join(args.Dir, source)].HasMain {
			continue
		}
		binaries = append(binaries, binaryRef{
			kind:       KindTsBinary,
			name:       name,
			entryPoint: source,
			deps:       []string{":" + libName},
		})
		detectedBinaries[source] = true
	}
	for _, source := range libSrcs {
		source = filepath.ToSlash(source)
		if detectedBinaries[source] || binaryFiles[source] || !allRefs[filepath.Join(args.Dir, source)].HasMain {
			continue
		}
		name := binaryNameForSource(source, cfg)
		if existingNames[name] {
			continue
		}
		binaries = append(binaries, binaryRef{
			kind:       KindTsBinary,
			name:       name,
			entryPoint: source,
			deps:       []string{":" + libName},
		})
		detectedBinaries[filepath.ToSlash(source)] = true
	}
	var staleBinaryNames []string
	for _, source := range reservedSources {
		name := reservedBinaries[source]
		if !detectedBinaries[source] {
			staleBinaryNames = append(staleBinaryNames, name)
		}
	}
	sort.Strings(staleBinaryNames)
	for _, name := range staleBinaryNames {
		emptyRules = append(emptyRules, rule.NewRule(KindTsBinary, name))
	}

	if len(libSrcs) == 0 && len(testSrcs) == 0 && len(visualLibrarySrcs) == 0 && len(binaries) == 0 && len(parts.bundlerConfigs) == 0 {
		return language.GenerateResult{Empty: emptyRules}
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

	// Existing binaries get placeholders so Resolve can update their deps.
	// Generated binaries depend on the sibling library, which owns their source
	// and import closure, following the library-embedded-by-binary shape used by
	// Gazelle's Go extension.
	for _, b := range binaries {
		var imps []ImportStatement
		var globals []GlobalReference
		for _, f := range b.files {
			refs := allRefs[filepath.Join(args.Dir, f)]
			imps = append(imps, refs.Imports...)
			globals = append(globals, refs.Globals...)
		}
		r := rule.NewRule(b.kind, b.name)
		if !b.existing && b.entryPoint != "" {
			r.SetAttr("entry_point", b.entryPoint)
		}
		if !b.existing && len(b.srcs) > 0 {
			r.SetAttr("srcs", b.srcs)
		}
		if len(b.deps) > 0 {
			r.SetAttr("deps", b.deps)
		}
		genRules = append(genRules, r)
		genImports = append(genImports, ImportData{
			Imports:           imps,
			Globals:           globals,
			BinaryUsesLibrary: len(b.deps) > 0,
		})
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
		Empty:   emptyRules,
		Imports: genImports,
	}
}

func binaryEntryPointInLibrary(entryPoint string, srcs []string, librarySources map[string]bool) bool {
	if entryPoint == "" && len(srcs) == 1 {
		entryPoint = srcs[0]
	}
	return librarySources[filepath.ToSlash(entryPoint)]
}

// existingLibraryName avoids target-name collisions. It reuses the sole
// existing package library or follows Gazelle's Go import naming convention
// for command libraries by appending "_lib".
func existingLibraryName(c *config.Config, file *rule.File, cfg *tsConfig, defaultName string) string {
	if file == nil || cfg.libraryName != "" {
		return defaultName
	}

	taken := map[string]bool{}
	defaultNameTaken := false
	defaultNameTakenByBinary := false
	var libraries []string
	for _, r := range file.Rules {
		taken[r.Name()] = true
		if kindMatches(c, r.Kind(), KindTsLibrary) {
			libraries = append(libraries, r.Name())
			continue
		}
		if r.Name() == defaultName {
			defaultNameTaken = true
			defaultNameTakenByBinary = isManagedBinary(c, r)
		}
	}

	if !defaultNameTaken {
		return defaultName
	}
	if len(libraries) == 1 {
		return libraries[0]
	}
	// Preserve Gazelle's existing merge behavior for unrelated target-name
	// collisions. Only invent a fallback when a binary would otherwise depend
	// on itself, or multiple package libraries make reuse ambiguous.
	if len(libraries) == 0 && !defaultNameTakenByBinary {
		return defaultName
	}
	commandLibraryName := defaultName + commandLibrarySuffix
	if !taken[commandLibraryName] {
		return commandLibraryName
	}
	for suffix := 2; ; suffix++ {
		name := fmt.Sprintf("%s_%d", commandLibraryName, suffix)
		if !taken[name] {
			return name
		}
	}
}

func binaryNameForSource(source string, cfg *tsConfig) string {
	name := filepath.ToSlash(source)
	for _, extension := range sortedUniqueExtensions(cfg.extensions) {
		name = strings.TrimSuffix(name, extension)
	}
	name = strings.NewReplacer("/", "_", ".", "_").Replace(name)
	return name + "_bin"
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

func isManagedBinary(c *config.Config, r *rule.Rule) bool {
	for _, kind := range managedBinaryKinds {
		if kindMatches(c, r.Kind(), kind) {
			return true
		}
	}
	return false
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
