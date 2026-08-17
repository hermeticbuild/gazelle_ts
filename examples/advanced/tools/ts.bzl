"""Project-specific wrappers around stock rules_ts/rules_js.

Gazelle is told to emit these via `# gazelle:map_kind` directives in
//BUILD.bazel:

    # gazelle:map_kind ts_library  myorg_ts_library //tools:ts.bzl
    # gazelle:map_kind ts_test     vitest_test      //tools:ts.bzl
    # gazelle:map_kind ts_visual_library    myorg_ts_visual_library   //tools:ts.bzl
    # gazelle:map_kind ts_binary   myorg_ts_binary  //tools:ts.bzl

The plugin emits TypeScript abstract kinds with no compile flags or
entry_point — those defaults belong here.

Note: rules_js's `@npm//:vitest/package_json.bzl` re-exports the auto-
generated bin macros under a single `bin = struct(...)` symbol — so the
inner `vitest_test` isn't directly load()-able from there. We re-bind it
here so map_kind can target a single load path.
"""

load("@aspect_rules_js//js:defs.bzl", _js_binary = "js_binary")
load("@aspect_rules_ts//ts:defs.bzl", _ts_project = "ts_project")
load("@npm//:vitest/package_json.bzl", _vitest_bin = "bin")

def _has_implementation_source(srcs):
    for src in srcs:
        if not (src.endswith(".d.ts") or src.endswith(".d.mts") or src.endswith(".d.cts")):
            return True
    return False

def myorg_ts_library(name, srcs, tsconfig_types = None, **kwargs):
    """Project defaults baked in: shared tsconfig + project-references
    compile flags. The plugin doesn't emit these on ts_library."""
    has_implementation_source = _has_implementation_source(srcs)
    _ts_project(
        name = name,
        srcs = srcs,
        composite = has_implementation_source,
        declaration = True,
        declaration_map = True,
        source_map = True,
        tsconfig = "//:tsconfig",
        # The shared config enables composite globally, but declaration-only
        # projects do not emit the build-info output that rules_ts predicts.
        validate = has_implementation_source,
        **kwargs
    )

def myorg_ts_binary(name, tsconfig_types = None, **kwargs):
    """Thin wrapper over js_binary for TS entry points. A real consumer
    would set default NODE_OPTIONS, a wrapping launcher script, or
    whatever house style the binaries should run with. The plugin
    auto-manages `data` from the rule's entry_point/srcs imports."""
    _js_binary(name = name, **kwargs)

def myorg_ts_visual_library(name, srcs, deps = [], tsconfig_types = None, **kwargs):
    """Visual library typecheck target with project defaults."""
    _ts_project(
        name = name,
        srcs = srcs,
        deps = deps,
        composite = True,
        declaration = True,
        declaration_map = True,
        source_map = True,
        tsconfig = "//:tsconfig",
        **kwargs
    )

# vitest_test auto-discovers test files via the runner's own config. The
# generated ts_test shape keeps entrypoints, import deps, and runtime fixtures
# separate; rules_js wants them together in runfiles.
def vitest_test(name, srcs, deps = [], data = [], tsconfig_types = None, **kwargs):
    _vitest_bin.vitest_test(name = name, data = srcs + deps + data, **kwargs)
