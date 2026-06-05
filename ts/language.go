// Package ts implements a Gazelle language extension for TypeScript packages.
//
// It generates abstract rule kinds (all loaded from
// @gazelle_ts//ts:defs.bzl, with filegroup fallbacks that print a warning
// when no map_kind is configured):
//
//   - ts_library          for libraries
//   - ts_test             for tests (assumes a multi-entry runner; no entry_point)
//   - ts_visual_library  for visual files (`*.story.tsx`, `*.visual.tsx`)
//   - ts_bundler_config   for files matched by ts_bundler_config_pattern
//
// Consumers wire each to a concrete macro:
//
//	# gazelle:map_kind ts_library         myrepo_ts_library //tools:ts.bzl
//	# gazelle:map_kind ts_test            vitest_test       //tools:ts.bzl
//	# gazelle:map_kind ts_visual_library   myrepo_ts_visual_library //tools:ts.bzl
//	# gazelle:map_kind ts_bundler_config  myrepo_bundler_config //tools:ts.bzl
//
// The plugin operates in Gazelle's three-phase pipeline:
//
//  1. GenerateRules (generate.go): scan .ts/.tsx files, extract imports via
//     the import_extractor cgo FFI, create/update rules.
//  2. Imports (imports.go): register rules in the RuleIndex so other
//     packages can resolve their imports against ours.
//  3. Resolve (resolve.go): convert parsed imports into Bazel deps labels.
//
// All configuration lives in BUILD-file directives (see configure.go); see
// README.md for the full list and examples.
package ts

import (
	"github.com/bazelbuild/bazel-gazelle/label"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// languageName is the unique identifier for this Gazelle extension. It must
// match the prefix used in directive keys (ts_enabled, ts_library_name, …).
const languageName = "ts"

// tsLang implements the language.Language interface from Gazelle. It carries
// cached package.json data used during import resolution.
type tsLang struct {
	// packageDeps is a set of all npm package names from the root package.json
	// (dependencies + devDependencies + optionalDependencies).
	packageDeps map[string]bool

	// subpathImportsMap stores the "imports" field from the root package.json,
	// e.g. `"#packages/*": ["./bazel-bin/packages/*", "./packages/*"]`.
	// Values are ordered fallbacks.
	subpathImportsMap map[string][]string
}

// NewLanguage creates a new TypeScript Gazelle language extension.
func NewLanguage() language.Language {
	return &tsLang{
		packageDeps:       make(map[string]bool),
		subpathImportsMap: make(map[string][]string),
	}
}

func (l *tsLang) Name() string { return languageName }

// Embeds returns nil — TypeScript doesn't use Bazel's rule embedding mechanism.
func (l *tsLang) Embeds(r *rule.Rule, from label.Label) []label.Label { return nil }
