package ts

import (
	"flag"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// All directives this plugin recognizes. Keep in sync with README.md.
//
// Notably absent: ts_library_kind / ts_test_kind / ts_project_references.
// `# gazelle:map_kind` subsumes the kind overrides (and additionally lets
// you pin the load path). Project-references compile flags belong in the
// wrapper macro behind ts_library, not in the plugin's emitted attrs.
const (
	directiveEnabled              = "ts_enabled"
	directiveLibraryName          = "ts_library_name"
	directiveTestName             = "ts_test_name"
	directiveVisualModuleName     = "ts_visual_module_name"
	directiveVisibility           = "ts_visibility"
	directiveTestPattern          = "ts_test_pattern"
	directiveVisualModulePattern  = "ts_visual_module_pattern"
	directiveExtension            = "ts_extension"
	directiveNpmLinkPattern       = "ts_npm_link_pattern"
	directiveTestData             = "ts_test_data"
	directiveTsconfigTypes        = "ts_tsconfig_types"
	directiveResolveGlobal        = "ts_resolve_global"
	directiveBundlerConfigPattern = "ts_bundler_config_pattern"
)

// RegisterFlags is a no-op — all configuration is via BUILD-file directives.
func (l *tsLang) RegisterFlags(fs *flag.FlagSet, cmd string, c *config.Config) {}

// CheckFlags is a no-op — there are no flags to validate.
func (l *tsLang) CheckFlags(fs *flag.FlagSet, c *config.Config) error { return nil }

// KnownDirectives returns the directive keys this plugin reads. Gazelle
// silently ignores any directive whose key isn't in this list.
func (l *tsLang) KnownDirectives() []string {
	return []string{
		directiveEnabled,
		directiveLibraryName,
		directiveTestName,
		directiveVisualModuleName,
		directiveVisibility,
		directiveTestPattern,
		directiveVisualModulePattern,
		directiveExtension,
		directiveNpmLinkPattern,
		directiveTestData,
		directiveTsconfigTypes,
		directiveResolveGlobal,
		directiveBundlerConfigPattern,
	}
}

// Configure builds the per-directory config by cloning the parent config and
// applying any directives present in the current BUILD file. At the repo root
// it also loads package.json data for import resolution.
func (l *tsLang) Configure(c *config.Config, rel string, f *rule.File) {
	var cfg *tsConfig
	if raw, ok := c.Exts[languageName]; ok {
		cfg = raw.(*tsConfig).clone()
	} else {
		cfg = newTsConfig()
	}

	if f != nil {
		for _, d := range f.Directives {
			applyDirective(cfg, d)
		}
	}

	c.Exts[languageName] = cfg

	if rel == "" {
		l.loadPackageJSONDeps(c.RepoRoot)
	}
}

func applyDirective(cfg *tsConfig, d rule.Directive) {
	val := strings.TrimSpace(d.Value)
	switch d.Key {
	case directiveEnabled:
		cfg.enabled = parseBool(val, cfg.enabled)
	case directiveLibraryName:
		if val != "" {
			cfg.libraryName = val
		}
	case directiveTestName:
		if val != "" {
			cfg.testName = val
		}
	case directiveVisualModuleName:
		if val != "" {
			cfg.visualModuleName = val
		}
	case directiveVisibility:
		if val != "" {
			cfg.visibility = splitFields(val)
		}
	case directiveTestPattern:
		if val != "" {
			cfg.testPatterns = appendUnique(cfg.testPatterns, val)
		}
	case directiveVisualModulePattern:
		if val != "" {
			cfg.visualModulePatterns = appendUnique(cfg.visualModulePatterns, val)
		}
	case directiveExtension:
		if val != "" {
			cfg.extensions = appendUnique(cfg.extensions, val)
		}
	case directiveNpmLinkPattern:
		if val != "" {
			cfg.npmLinkPattern = val
		}
	case directiveTestData:
		if val != "" {
			cfg.testData = appendUnique(cfg.testData, val)
		}
	case directiveTsconfigTypes:
		for _, typ := range splitFields(val) {
			cfg.tsconfigTypes = appendUnique(cfg.tsconfigTypes, typ)
		}
	case directiveResolveGlobal:
		fields := strings.Fields(val)
		if len(fields) != 2 {
			return
		}
		cfg.globalResolves[fields[0]] = fields[1]
	case directiveBundlerConfigPattern:
		// Format: `<glob> <name>`, e.g. `vite.config.* vite_config`. The
		// glob is matched against package-relative file paths; <name> is
		// the literal Bazel target name for the emitted ts_bundler_config
		// rule. Malformed entries (missing one or both fields) are silently
		// ignored — keeps gazelle runs robust against in-progress edits.
		fields := strings.Fields(val)
		if len(fields) != 2 {
			return
		}
		spec := bundlerConfigSpec{Pattern: fields[0], Name: fields[1]}
		for _, existing := range cfg.bundlerConfigSpecs {
			if existing == spec {
				return
			}
		}
		cfg.bundlerConfigSpecs = append(cfg.bundlerConfigSpecs, spec)
	}
}

func parseBool(val string, fallback bool) bool {
	switch strings.ToLower(val) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return fallback
}

func splitFields(s string) []string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func appendUnique(slice []string, val string) []string {
	for _, v := range slice {
		if v == val {
			return slice
		}
	}
	return append(slice, val)
}
