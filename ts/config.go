package ts

// Default values applied when no directive overrides them. These match the
// shape a "small typical" TS package emits with stock rules_ts/rules_js.
const (
	// Empty default means "use the package's directory basename" — see
	// resolveRuleNames in generate.go. This way //apps/web:web shortens to
	// //apps/web, the most natural Bazel idiom.
	defaultLibraryName      = ""
	defaultTestName         = ""
	defaultVisualModuleName = ""
	defaultNpmLinkPattern   = "//:node_modules/{pkg}"

	// KindTsLibrary is the abstract library kind the plugin emits. Consumers
	// must `# gazelle:map_kind ts_library <macro> <load_path>` to a concrete
	// macro; the fallback in @gazelle_ts//ts:defs.bzl is a filegroup that
	// keeps the BUILD parseable but doesn't typecheck.
	KindTsLibrary = "ts_library"

	// KindTsTest is the abstract test kind. The plugin emits it with no
	// entry_point — consumers map_kind to vitest_test, jest_test, or any
	// multi-entry runner that auto-discovers from `data`. The fallback in
	// @gazelle_ts//ts:defs.bzl is a filegroup so the BUILD still loads.
	KindTsTest = "ts_test"

	// KindTsVisualModule is the abstract visual-module kind. It keeps
	// `*.story.tsx` files out of the main library while preserving their own
	// import deps.
	KindTsVisualModule = "ts_visual_module"
)

// Default test-file patterns and source-file extensions. Patterns are matched
// against the file path relative to the package directory.
var (
	defaultTestPatterns = []string{
		"*.test.ts",
		"*.test.tsx",
		"tests/**",
		"test/**",
		"**/*.test.ts",
		"**/*.test.tsx",
		"**/*.spec.ts",
		"**/*.spec.tsx",
	}
	defaultVisualModulePatterns = []string{
		"*.story.tsx",
		"**/*.story.tsx",
	}
	defaultExtensions = []string{".ts", ".tsx"}
	defaultVisibility = []string{"//visibility:public"}

	// Only ambient type packages belong in compilerOptions.types. Module
	// declaration packages such as @types/react or @types/lodash are still
	// needed as deps, but listing them in types unnecessarily narrows global
	// type discovery. Node builtins are the common ambient case.
	defaultTsconfigTypes = []string{"node"}
)

// tsConfig holds per-directory configuration. Gazelle calls Configure() for
// each directory during the walk, building up the config by cloning the
// parent and applying any directives in the directory's BUILD file.
type tsConfig struct {
	enabled bool

	// libraryName / testName / visualModuleName are the generated rule names.
	libraryName      string
	testName         string
	visualModuleName string

	// visibility is the list of labels emitted on the library rule.
	visibility []string

	// testPatterns: glob-style patterns deciding which files are tests.
	testPatterns []string

	// visualModulePatterns: glob-style patterns deciding which files become
	// visual modules.
	visualModulePatterns []string

	// extensions: file extensions treated as TypeScript source.
	extensions []string

	// npmLinkPattern is the template used for npm package labels, e.g.
	// `//:node_modules/{pkg}`. The literal `{pkg}` is replaced with the
	// resolved package name.
	npmLinkPattern string

	// testData is added to every emitted test rule's `data` attr.
	testData []string

	// tsconfigTypes is added to every emitted TypeScript compilation rule's
	// `tsconfig_types` attr when the corresponding @types/* package is
	// resolved. This is an allowlist of ambient type packages, not a list of
	// every @types/* dependency.
	tsconfigTypes []string

	// globalResolves maps referenced globals (e.g. process, chrome) to the
	// ambient type label that provides them.
	globalResolves map[string]string

	// bundlerConfigSpecs lists the bundler/tooling config files held out of
	// the library compilation unit, each with its own emitted target name.
	// Each spec maps a glob pattern to the Bazel target name to emit; matched
	// files are excluded from libSrcs/testSrcs and grouped under their spec.
	bundlerConfigSpecs []bundlerConfigSpec
}

// bundlerConfigSpec is one entry of the ts_bundler_config_pattern directive:
// a glob plus the target name to emit for files matching that glob.
type bundlerConfigSpec struct {
	Pattern string
	Name    string
}

// newTsConfig returns a config populated with all defaults.
func newTsConfig() *tsConfig {
	return &tsConfig{
		enabled:              true,
		libraryName:          defaultLibraryName,
		testName:             defaultTestName,
		visualModuleName:     defaultVisualModuleName,
		visibility:           append([]string(nil), defaultVisibility...),
		testPatterns:         append([]string(nil), defaultTestPatterns...),
		visualModulePatterns: append([]string(nil), defaultVisualModulePatterns...),
		extensions:           append([]string(nil), defaultExtensions...),
		npmLinkPattern:       defaultNpmLinkPattern,
		tsconfigTypes:        append([]string(nil), defaultTsconfigTypes...),
		globalResolves:       map[string]string{},
	}
}

// clone makes a deep copy so child directories inherit but can override
// without mutating the parent.
func (c *tsConfig) clone() *tsConfig {
	cp := *c
	cp.visibility = append([]string(nil), c.visibility...)
	cp.testPatterns = append([]string(nil), c.testPatterns...)
	cp.visualModulePatterns = append([]string(nil), c.visualModulePatterns...)
	cp.extensions = append([]string(nil), c.extensions...)
	cp.testData = append([]string(nil), c.testData...)
	cp.tsconfigTypes = append([]string(nil), c.tsconfigTypes...)
	cp.globalResolves = copyStringMap(c.globalResolves)
	cp.bundlerConfigSpecs = append([]bundlerConfigSpec(nil), c.bundlerConfigSpecs...)
	return &cp
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
