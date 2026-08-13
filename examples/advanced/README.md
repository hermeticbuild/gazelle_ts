# examples/monorepo

A self-contained Bazel workspace that exercises the `ts` plugin against a representative TypeScript monorepo using the **real** `aspect_rules_ts` + `aspect_rules_js` rules — no stubs.

## Layout

```
.
├── apps/
│   ├── web/       # browser app — React + react-query + cross-package refs
│   └── cli/       # node CLI — node:fs + cross-package refs + synthetic pkg + virtual module
└── packages/
    ├── core/                    # base package, no internal deps
    ├── synthetic/               # genrule-built npm_package + npm_link_package
    └── utils/
        ├── string/              # depends on packages/core
        └── math/deep/           # depends on packages/core, deeply nested
```

`.ts` files live directly in each package directory (no `src/` subdirectory) — gazelle treats every directory containing `.ts` files as a Bazel package and emits one `ts_library` per directory (mapped via `# gazelle:map_kind` to `myorg_ts_library` in `tools/ts.bzl`). Every `*.ts` file demonstrates a different mix of import patterns. The synthetic package shows how Bazel-generated npm packages (not in `package.json`) are wired via Gazelle's native `# gazelle:resolve_regexp` directive.

## What this verifies

- `ts_library` rules with cross-package `deps` resolved across arbitrary nesting depth; the wrapper macro turns implementation sources into `ts_project` targets with TS project references (`composite = True`) while keeping declaration-only targets non-composite.
- `ts_test` targets generated for `*.test.ts` files; mapped via `# gazelle:map_kind` to `vitest_test` (a multi-entry runner that auto-discovers from `data`).
- npm packages auto-paired with `@types/*` when present in `package.json`.
- Node.js builtins resolve to `@types/node`.
- Synthetic Bazel-generated packages link into `//:node_modules/@myrepo_generated/synthetic` via `npm_link_package` and resolve through the `resolve_regexp` directive.
- The full chain — pnpm-lock translation, `npm_link_all_packages`, the `ts_library` wrapper compiling via tsc, and `vitest_test` — composes end-to-end.

## Running

```bash
# Regenerate BUILD files
bazel run //:gazelle

# Build everything (compiles all TS via tsc)
bazel build //...

# Run tests
bazel test //...
```

## Key wiring

| File | What it sets up |
|---|---|
| [`MODULE.bazel`](MODULE.bazel) | `aspect_rules_js`, `aspect_rules_ts`, `rules_nodejs`, `npm_translate_lock`, `rules_ts_ext.deps()`. Pins Node 22.11 and a local override on the parent `gazelle_ts` repo. |
| [`.bazelrc`](.bazelrc) | `--@aspect_rules_ts//ts:default_to_tsc_transpiler=True` so every `ts_project` defaults to tsc; `--test_env=NODE_OPTIONS=--experimental-transform-types` so `js_test` can run `.ts` files directly. |
| [`tsconfig.json`](tsconfig.json) | `composite`, `declaration`, `declarationMap`, `sourceMap` on (matching the wrapper macro's emitted attrs), `paths` mirroring `package.json` `imports`. |
| [`BUILD.bazel`](BUILD.bazel) | `npm_link_all_packages` (pnpm-driven), `npm_link_package` for the synthetic package, gazelle directives (`ts_npm_link_pattern`, `resolve_regexp`, `map_kind`s). |
| [`packages/synthetic/BUILD.bazel`](packages/synthetic/BUILD.bazel) | Three `genrule`s producing `index.js` / `package.json` / `index.d.ts`, an `npm_package` wrapping them. The root linker entry maps it to `//:node_modules/@myrepo_generated/synthetic`. |
| [`tools/ts.bzl`](tools/ts.bzl) | Project wrappers (`myorg_ts_library`, `vitest_test`) that generated BUILDs route to via `# gazelle:map_kind`. Forwards to stock `ts_project` / `js_test`; declaration-only projects disable composite output and validation against the shared composite config. |
| [`tools/BUILD.bazel`](tools/BUILD.bazel) | A `js_library` named `mystery` that the `# gazelle:resolve ts ts mystery:banner //tools:mystery` directive maps an arbitrary import string to. |

### Demonstrated directives

This example is the kitchen-sink — you can see all three of gazelle's load-time customization knobs at work:

- **`# gazelle:map_kind`** in [`BUILD.bazel`](BUILD.bazel) routes the abstract `ts_library` → `myorg_ts_library`, `ts_test` → `vitest_test`, and `ts_binary` → `myorg_ts_binary` (all from `//tools:ts.bzl`). Every emitted BUILD file uses the wrapper kinds.
- **`# gazelle:resolve`** in [`BUILD.bazel`](BUILD.bazel) maps the virtual import `mystery:banner` to `//tools:mystery`. `apps/cli/main.ts` imports it; the type comes from a co-located `apps/cli/types.d.ts`.
- **`# keep`** is shown in [`examples/composite/apps/web/BUILD.bazel`](../composite/apps/web/BUILD.bazel) — a `users.json` fixture is hand-added to `data` and survives every `bazel run //:gazelle`.

## Test runner notes

The example uses a smoke `*.test.ts` that runs directly under Node 22's experimental type-stripping. For real test runners (vitest, jest, mocha), the typical pattern is:

```starlark
# gazelle:map_kind js_test vitest_test //tools:vitest.bzl
```

Your `vitest_test` macro takes the same `data` + `entry_point` attrs the plugin emits and dispatches them to your runner. Subpath imports (`#packages/*` style) at runtime require either bundling the test or shipping `package.json` with the test sandbox; that's runner-specific.
