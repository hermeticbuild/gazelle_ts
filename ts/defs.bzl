"""Stubs for the abstract rule kinds emitted by the gazelle_ts plugin.

`ts_library`, `ts_test`, `ts_visual_module`, `ts_binary`, and
`ts_bundler_config` are intentionally abstract: the plugin emits them with
this load path, and the consumer must override each with a project-specific
macro via `# gazelle:map_kind`. The wrappers typically forward to
`ts_project`, `js_test`/`vitest_test`/`jest_test`, Storybook-specific
compile/check targets, or `js_binary` with project-specific defaults baked in
(transpiler, tsconfig, visibility, project-references compile flags,
entry_point handling, launcher).

`ts_binary` is the binary-flavored sibling of `ts_library`: hand-written
by the user (we never generate one), but its `data` attr is auto-managed
from the rule's `entry_point` / `srcs` imports. Same lifecycle as stock
`js_binary`, just under the gazelle_ts namespace so consumers can swap
implementations through map_kind without rewriting their gazelle config.

The gazelle_ts module deliberately does not take a transitive
`aspect_rules_ts` or `aspect_rules_js` dependency, so the macros can't
live here.

The fallbacks below collect matched files into a filegroup with a debug
print so the BUILD still loads and gazelle can run — but the files are
NOT typechecked or executed. To get a real compilation/test/binary unit,
add to your root BUILD:

    # gazelle:map_kind ts_library         <macro> <load_path>
    # gazelle:map_kind ts_test            <macro> <load_path>
    # gazelle:map_kind ts_visual_module   <macro> <load_path>
    # gazelle:map_kind ts_binary          <macro> <load_path>
    # gazelle:map_kind ts_bundler_config  <macro> <load_path>

and re-run gazelle. See `examples/advanced/tools/ts.bzl` for one-line
wrappers.
"""

def _abstract_kind(kind, name, srcs_or_data):
    # buildifier: disable=print
    print(
        kind + "('" + name + "') is using the abstract-kind fallback (no typecheck/execute). " +
        "Add `# gazelle:map_kind " + kind + " <macro> <load_path>` and re-run gazelle.",
    )
    native.filegroup(name = name, srcs = srcs_or_data)

def ts_library(name, srcs, **_kwargs):
    _abstract_kind("ts_library", name, srcs)

def ts_test(name, srcs, deps = [], data = [], **_kwargs):
    _abstract_kind("ts_test", name, srcs + deps + data)

def ts_visual_module(name, srcs, deps = [], **_kwargs):
    _abstract_kind("ts_visual_module", name, srcs + deps)

def ts_binary(name, data = None, **_kwargs):
    _abstract_kind("ts_binary", name, data or [])

def ts_bundler_config(name, srcs, **_kwargs):
    _abstract_kind("ts_bundler_config", name, srcs)
