# examples/cross_compile

Validates that downstream consumers can cross-compile a `gazelle_binary` linking `@gazelle_ts//ts` to darwin targets via `--platforms`. **No TypeScript source, no BUILD generation** — the only purpose is to exercise the toolchain/repo-visibility plumbing that downstream modules inherit from `gazelle_ts`.

## What this guards against

`gazelle_ts` links a Rust staticlib (`//crates/import_extractor:import_extractor_static`) into the gazelle plugin via cgo. Under rules_rs >= 0.0.64, the macOS Rust toolchain emits a `@macos_sdk//sysroot` reference that requires `@macos_sdk` to be visible *from `@@gazelle_ts+`* — the consumer's `use_repo` doesn't help, because module visibility is scoped to the module that owns the reference. Without `gazelle_ts`'s own `use_repo(osx, "macos_sdk")`, every cross-compile to darwin fails at analysis time with:

```
No repository visible as '@macos_sdk' from repository '@@gazelle_ts+'
```

This example uses the same `rules_rs` version as the main module so its cross-compile analysis catches toolchain visibility regressions after dependency upgrades.

## Try it

```bash
bazel build --platforms=@rules_rs//rs/platforms:aarch64-apple-darwin //:gazelle_bin
bazel build --platforms=@rules_rs//rs/platforms:x86_64-apple-darwin  //:gazelle_bin
```

Both should analyze and (on a darwin host) link cleanly. On a Linux runner without an Apple cc toolchain registered, use `--nobuild` to limit the check to analysis — that's where the `@macos_sdk` regression manifests.
