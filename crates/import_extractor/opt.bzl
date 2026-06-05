"""Rust target wrappers for cgo-compatible import_extractor builds.

The gazelle plugin's go_library links the import_extractor rlib via cgo.
Rust 1.95's allocator symbols are mangled, so the rlib must be compiled
with rules_rust's matching allocator-library setting. Pinning that here
keeps consumers and BCR from needing a top-level command-line flag.

The staticlib wrapper intentionally follows the caller's compilation mode.
Release validation selects optimized mode with `--config=opt`.
"""

load("@rules_rs//rs:rust_library.bzl", "rust_library")
load("@rules_rs//rs:rust_static_library.bzl", "rust_static_library")
load("@rules_rust//rust:rust_common.bzl", "COMMON_PROVIDERS")
load("@with_cfg.bzl", "with_cfg")

_ALLOCATOR_MANGLED_SYMBOLS = Label("@rules_rust//rust/settings:experimental_use_allocator_libraries_with_mangled_symbols")

cgo_rust_library, _cgo_rust_library_internal = (
    with_cfg(rust_library, extra_providers = COMMON_PROVIDERS)
        .set(_ALLOCATOR_MANGLED_SYMBOLS, True)
        .build()
)

cgo_rust_static_library, _cgo_rust_static_library_internal = (
    with_cfg(rust_static_library)
        .set(_ALLOCATOR_MANGLED_SYMBOLS, True)
        .build()
)
