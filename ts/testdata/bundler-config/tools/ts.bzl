"""Concrete macros for the abstract kinds emitted by gazelle_ts.

The abstract TypeScript compile/check kinds forward to ts_project / js_test
with the project's defaults baked in.
"""

load("@aspect_rules_ts//ts:defs.bzl", "ts_project")
load("@aspect_rules_js//js:defs.bzl", "js_test")

def _project(name, srcs, tsconfig_types = None, **kwargs):
    ts_project(
        name = name,
        srcs = srcs,
        composite = True,
        declaration = True,
        declaration_map = True,
        source_map = True,
        tsconfig = "//:tsconfig",
        **kwargs
    )

def ts_library(name, srcs, tsconfig_types = None, **kwargs):
    _project(name = name, srcs = srcs, **kwargs)

def ts_bundler_config(name, srcs, **kwargs):
    _project(name = name, srcs = srcs, **kwargs)

def ts_test(name, srcs, deps = [], data = [], tsconfig_types = None, **kwargs):
    js_test(name = name, data = srcs + deps + data, entry_point = srcs[0], **kwargs)

def ts_visual_module(name, srcs, deps = [], tsconfig_types = None, **kwargs):
    _project(name = name, srcs = srcs, deps = deps, **kwargs)
