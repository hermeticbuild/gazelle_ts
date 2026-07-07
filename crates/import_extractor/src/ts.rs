// extract_imports.rs -- oxc-based TypeScript import extractor.
//
// Parses TypeScript/TSX files with oxc and walks the AST to collect all import paths.
// This is the core parsing logic used by the gazelle TS plugin to determine
// dependencies between TypeScript packages.
//
// Handles all TypeScript import forms:
//   - import declarations:    import { x } from 'module', import 'module'
//   - export-from:            export { x } from 'module'
//   - export-all:             export * from 'module'
//   - dynamic import:         import('module')  (oxc models this as ImportExpression)
//   - CommonJS require:       require('module')
//   - type-only imports:      import type { X } from 'module' (still extracted -- needed for type checking)
//
// The extracted paths are raw module specifiers (e.g., "react", "myorg/frontend/common").
// Resolution to Bazel labels happens on the Go side in resolve.go.

use oxc::ast_visit::{Visit, walk};
use oxc::semantic::{IsGlobalReference, Scoping, SemanticBuilder};
use oxc_allocator::Allocator;
use oxc_ast::ast::*;
use oxc_parser::Parser;
use oxc_span::SourceType;
use std::collections::HashSet;

/// Extract all import paths from a TypeScript/TSX file on disk.
pub fn extract_imports_from_file(path: &str) -> Result<Vec<String>, String> {
    let source_text =
        std::fs::read_to_string(path).map_err(|e| format!("Failed to read {path}: {e}"))?;
    Ok(extract_imports(path, &source_text))
}

pub struct ExtractedReferences {
    pub imports: Vec<String>,
    pub globals: Vec<String>,
}

pub fn extract_references_from_file(path: &str) -> Result<ExtractedReferences, String> {
    let source_text =
        std::fs::read_to_string(path).map_err(|e| format!("Failed to read {path}: {e}"))?;
    Ok(extract_references(path, &source_text))
}

/// Extract all import paths from TypeScript/TSX source code.
///
/// For malformed files, oxc performs error recovery and produces a partial AST.
/// We extract imports from whatever the parser could recover, which is the right
/// behavior for gazelle: partially-edited files during development still get
/// their valid imports resolved. Since gazelle runs as a pre-commit hook, the
/// file will typically be fixed before the next run.
pub fn extract_imports(path: &str, source_text: &str) -> Vec<String> {
    extract_references(path, source_text).imports
}

pub fn extract_references(path: &str, source_text: &str) -> ExtractedReferences {
    let allocator = Allocator::default();
    // SourceType::from_path sets jsx=true for .tsx, jsx=false for .ts/.mts/.cts.
    // Do NOT override with with_jsx(true) — it causes oxc to misparse TypeScript
    // generics (e.g. Promise<Foo | undefined>) as JSX opening tags in .ts files,
    // silently mangling the AST and losing imports from function bodies.
    let source_type = SourceType::from_path(path).unwrap_or_default();

    let ret = Parser::new(&allocator, source_text, source_type).parse();

    let semantic = SemanticBuilder::new().build(&ret.program).semantic;

    let mut visitor = ImportVisitor::new(semantic.scoping());
    visitor.visit_program(&ret.program);
    visitor.into_references()
}

/// AST visitor that collects import paths and global references from TypeScript source code.
struct ImportVisitor<'s> {
    scoping: &'s Scoping,
    imports: Vec<String>,
    globals: Vec<String>,
    seen_imports: HashSet<String>,
    seen_globals: HashSet<String>,
}

impl<'s> ImportVisitor<'s> {
    fn new(scoping: &'s Scoping) -> Self {
        Self {
            scoping,
            imports: Vec::new(),
            globals: Vec::new(),
            seen_imports: HashSet::new(),
            seen_globals: HashSet::new(),
        }
    }

    fn add(&mut self, path: &str) {
        if !path.is_empty() && self.seen_imports.insert(path.to_string()) {
            self.imports.push(path.to_string());
        }
    }

    fn add_global(&mut self, name: &str) {
        if !name.is_empty() && self.seen_globals.insert(name.to_string()) {
            self.globals.push(name.to_string());
        }
    }

    fn add_global_chain<'b>(&mut self, parts: impl IntoIterator<Item = &'b str>) {
        let mut chain = String::new();
        for (idx, part) in parts.into_iter().enumerate() {
            if idx > 0 {
                chain.push('.');
            }
            chain.push_str(part);
            if idx > 0 {
                self.add_global(&chain);
            }
        }
    }

    fn add_global_chain_including_first<'b>(&mut self, parts: impl IntoIterator<Item = &'b str>) {
        let mut chain = String::new();
        for (idx, part) in parts.into_iter().enumerate() {
            if idx > 0 {
                chain.push('.');
            }
            chain.push_str(part);
            self.add_global(&chain);
        }
    }

    fn add_static_member_globals(&mut self, expr: &StaticMemberExpression<'_>) -> bool {
        if !expression_root_is_global(&expr.object, self.scoping) {
            return false;
        }
        let Some(parts) = expression_chain(&expr.object) else {
            return false;
        };
        let parts = parts
            .iter()
            .chain([expr.property.name.as_str()].iter())
            .copied()
            .collect::<Vec<_>>();
        self.add_global_chain(parts.iter().copied());
        if parts
            .first()
            .is_some_and(|root| is_global_object_name(root))
            && parts.len() > 1
        {
            self.add_global_chain_including_first(parts.iter().skip(1).copied());
        }
        true
    }

    fn add_static_require_call(&mut self, expr: &CallExpression<'_>) {
        let Expression::Identifier(callee) = &expr.callee else {
            return;
        };
        if callee.name.as_str() != "require" || !callee.is_global_reference(self.scoping) {
            return;
        }
        let Some(Expression::StringLiteral(lit)) =
            expr.arguments.first().and_then(Argument::as_expression)
        else {
            return;
        };
        self.add(lit.value.as_str());
    }

    fn into_references(self) -> ExtractedReferences {
        ExtractedReferences {
            imports: self.imports,
            globals: self.globals,
        }
    }
}

fn is_global_object_name(name: &str) -> bool {
    matches!(name, "window" | "global" | "globalThis")
}

fn expression_root_is_global(expr: &Expression<'_>, scoping: &Scoping) -> bool {
    match expr {
        Expression::Identifier(ident) => ident.is_global_reference(scoping),
        Expression::MetaProperty(_) => true,
        Expression::StaticMemberExpression(member) => {
            expression_root_is_global(&member.object, scoping)
        }
        _ => false,
    }
}

fn expression_chain<'a>(expr: &'a Expression<'a>) -> Option<Vec<&'a str>> {
    match expr {
        Expression::Identifier(ident) => Some(vec![ident.name.as_str()]),
        Expression::MetaProperty(meta) => {
            Some(vec![meta.meta.name.as_str(), meta.property.name.as_str()])
        }
        Expression::StaticMemberExpression(member) => {
            let mut parts = expression_chain(&member.object)?;
            parts.push(member.property.name.as_str());
            Some(parts)
        }
        _ => None,
    }
}

fn type_name_root_is_global(name: &TSTypeName<'_>, scoping: &Scoping) -> bool {
    match name {
        TSTypeName::IdentifierReference(ident) => ident.is_global_reference(scoping),
        TSTypeName::QualifiedName(qualified) => type_name_root_is_global(&qualified.left, scoping),
        TSTypeName::ThisExpression(_) => false,
    }
}

fn type_name_chain<'a>(name: &'a TSTypeName<'a>) -> Option<Vec<&'a str>> {
    match name {
        TSTypeName::IdentifierReference(ident) => Some(vec![ident.name.as_str()]),
        TSTypeName::QualifiedName(qualified) => qualified_name_chain(qualified),
        TSTypeName::ThisExpression(_) => None,
    }
}

fn qualified_name_chain<'a>(name: &'a TSQualifiedName<'a>) -> Option<Vec<&'a str>> {
    let mut parts = type_name_chain(&name.left)?;
    parts.push(name.right.name.as_str());
    Some(parts)
}

impl<'a> Visit<'a> for ImportVisitor<'_> {
    // import ... from 'module' | import 'module'
    fn visit_import_declaration(&mut self, decl: &ImportDeclaration<'a>) {
        self.add(decl.source.value.as_str());
        walk::walk_import_declaration(self, decl);
    }

    // export { x } from 'module'
    fn visit_export_named_declaration(&mut self, decl: &ExportNamedDeclaration<'a>) {
        if let Some(ref source) = decl.source {
            self.add(source.value.as_str());
        }
        walk::walk_export_named_declaration(self, decl);
    }

    // export * from 'module'
    fn visit_export_all_declaration(&mut self, decl: &ExportAllDeclaration<'a>) {
        self.add(decl.source.value.as_str());
        walk::walk_export_all_declaration(self, decl);
    }

    // import('module') -- oxc models dynamic imports as ImportExpression
    fn visit_import_expression(&mut self, expr: &ImportExpression<'a>) {
        if let Expression::StringLiteral(lit) = &expr.source {
            self.add(lit.value.as_str());
        }
        walk::walk_import_expression(self, expr);
    }

    // require('module') -- only static, unshadowed string-literal CommonJS calls.
    fn visit_call_expression(&mut self, expr: &CallExpression<'a>) {
        self.add_static_require_call(expr);
        walk::walk_call_expression(self, expr);
    }

    // import('module').Type -- oxc models inline type imports as TSImportType
    fn visit_ts_import_type(&mut self, it: &TSImportType<'a>) {
        self.add(it.source.value.as_str());
        walk::walk_ts_import_type(self, it);
    }

    fn visit_identifier_reference(&mut self, ident: &IdentifierReference<'a>) {
        if ident.is_global_reference(self.scoping) {
            self.add_global(ident.name.as_str());
        }
        walk::walk_identifier_reference(self, ident);
    }

    fn visit_static_member_expression(&mut self, expr: &StaticMemberExpression<'a>) {
        if self.add_static_member_globals(expr) {
            return;
        }
        walk::walk_static_member_expression(self, expr);
    }

    fn visit_ts_qualified_name(&mut self, name: &TSQualifiedName<'a>) {
        if !type_name_root_is_global(&name.left, self.scoping) {
            walk::walk_ts_qualified_name(self, name);
            return;
        }
        let Some(parts) = qualified_name_chain(name) else {
            walk::walk_ts_qualified_name(self, name);
            return;
        };
        self.add_global_chain(parts);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn empty_file() {
        assert_eq!(extract_imports("test.ts", ""), Vec::<String>::new());
    }

    #[test]
    fn static_imports() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import foo from 'foo';
            import { bar } from 'bar';
            import * as baz from 'baz';
        "#,
        );
        assert_eq!(imports, vec!["foo", "bar", "baz"]);
    }

    #[test]
    fn side_effect_import() {
        let imports = extract_imports("test.ts", "import 'polyfill';");
        assert_eq!(imports, vec!["polyfill"]);
    }

    #[test]
    fn type_imports() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import type { Foo } from 'foo';
            import { type Bar } from 'bar';
        "#,
        );
        assert_eq!(imports, vec!["foo", "bar"]);
    }

    #[test]
    fn export_from() {
        let imports = extract_imports(
            "test.ts",
            r#"
            export { x } from 'foo';
            export * from 'bar';
            export type { Baz } from 'baz';
        "#,
        );
        assert_eq!(imports, vec!["foo", "bar", "baz"]);
    }

    #[test]
    fn dynamic_import() {
        let imports = extract_imports("test.ts", "const m = await import('lazy');");
        assert_eq!(imports, vec!["lazy"]);
    }

    #[test]
    fn globals() {
        let refs = extract_references(
            "test.ts",
            r#"
            process.env.NODE_ENV;
            chrome.runtime.sendMessage('hi');
            google.accounts.id.initialize({});
            const local = process;
        "#,
        );
        assert_eq!(refs.imports, Vec::<String>::new());
        assert_eq!(
            refs.globals,
            vec![
                "process.env",
                "process.env.NODE_ENV",
                "chrome.runtime",
                "chrome.runtime.sendMessage",
                "google.accounts",
                "google.accounts.id",
                "google.accounts.id.initialize",
                "process",
            ]
        );
    }

    #[test]
    fn qualified_type_names_are_extracted_as_global_chains() {
        let refs = extract_references(
            "test.ts",
            "export type PickedFile = google.picker.DocumentObject;",
        );
        assert_eq!(
            refs.globals,
            vec!["google.picker", "google.picker.DocumentObject"]
        );
    }

    #[test]
    fn bare_type_annotations_are_extracted_as_globals() {
        let refs = extract_references("test.ts", "export const foo: Bar = {};");
        assert_eq!(refs.globals, vec!["Bar"]);
    }

    #[test]
    fn local_values_do_not_emit_global_chains() {
        let refs = extract_references(
            "test.ts",
            r#"
            import chrome from 'chrome';

            const process = { env: { NODE_ENV: 'test' } };
            const window = { gapi: { load() {} } };

            process.env.NODE_ENV;
            chrome.runtime.sendMessage('hi');
            window.gapi.load('picker', () => {});
            "#,
        );
        assert_eq!(refs.imports, vec!["chrome"]);
        assert_eq!(refs.globals, Vec::<String>::new());
    }

    #[test]
    fn global_object_members_emit_projected_global_chains() {
        let refs = extract_references(
            "test.ts",
            r#"
            window.gapi;
            window.gapi.load('picker', () => {});
            global.process.env.NODE_ENV;
            globalThis.chrome.runtime.sendMessage('hi');
            "#,
        );
        assert_eq!(
            refs.globals,
            vec![
                "window.gapi",
                "gapi",
                "window.gapi.load",
                "gapi.load",
                "global.process",
                "global.process.env",
                "global.process.env.NODE_ENV",
                "process",
                "process.env",
                "process.env.NODE_ENV",
                "globalThis.chrome",
                "globalThis.chrome.runtime",
                "globalThis.chrome.runtime.sendMessage",
                "chrome",
                "chrome.runtime",
                "chrome.runtime.sendMessage",
            ]
        );
    }

    #[test]
    fn local_namespaces_do_not_emit_qualified_global_chains() {
        let refs = extract_references(
            "test.ts",
            r#"
            namespace google {
                export namespace picker {
                    export type DocumentObject = {};
                }
            }

            export type PickedFile = google.picker.DocumentObject;
            "#,
        );
        assert_eq!(refs.globals, Vec::<String>::new());
    }

    #[test]
    fn import_meta_env_is_extracted_as_a_global_chain() {
        let refs = extract_references("test.ts", "import.meta.env.VITE_FOO;");
        assert_eq!(
            refs.globals,
            vec!["import.meta", "import.meta.env", "import.meta.env.VITE_FOO"]
        );
    }

    #[test]
    fn tsx_file() {
        let imports = extract_imports(
            "test.tsx",
            r#"
            import React from 'react';
            export function App() { return <div />; }
        "#,
        );
        assert_eq!(imports, vec!["react"]);
    }

    #[test]
    fn deduplicates() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import { a } from 'foo';
            import { b } from 'foo';
        "#,
        );
        assert_eq!(imports, vec!["foo"]);
    }

    #[test]
    fn workspace_imports() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import { x } from 'myorg/frontend/common';
            import type { User } from 'myorg/frontend/types';
        "#,
        );
        assert_eq!(
            imports,
            vec!["myorg/frontend/common", "myorg/frontend/types"]
        );
    }

    #[test]
    fn mixed_imports() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import React from 'react';
            import { helper } from 'myorg/frontend/utils';
            import type { Props } from 'myorg/frontend/types';
            export * from './utils.js';
            const lazy = await import('lazy-module');
        "#,
        );
        assert_eq!(
            imports,
            vec![
                "react",
                "myorg/frontend/utils",
                "myorg/frontend/types",
                "./utils.js",
                "lazy-module",
            ]
        );
    }

    #[test]
    fn ignores_comments() {
        let imports = extract_imports(
            "test.ts",
            r#"
            // import React from 'react';
            /* import { useState } from 'react'; */
            import { useEffect } from 'react-dom';
        "#,
        );
        assert_eq!(imports, vec!["react-dom"]);
    }

    #[test]
    fn ignores_string_literals() {
        let imports = extract_imports(
            "test.ts",
            r#"
            const code = "import React from 'react';";
            import { useState } from 'react-dom';
        "#,
        );
        assert_eq!(imports, vec!["react-dom"]);
    }

    #[test]
    fn side_effect_css() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import './styles.css';
            import '../reset.css';
            import logo from './logo.svg';
            import 'tailwindcss/base';
        "#,
        );
        assert_eq!(
            imports,
            vec![
                "./styles.css",
                "../reset.css",
                "./logo.svg",
                "tailwindcss/base"
            ]
        );
    }

    #[test]
    fn side_effect_polyfills() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import 'core-js/stable';
            import 'regenerator-runtime/runtime';
            import '@formatjs/intl-pluralrules/polyfill';
            import '@formatjs/intl-pluralrules/locale-data/en';
        "#,
        );
        assert_eq!(
            imports,
            vec![
                "core-js/stable",
                "regenerator-runtime/runtime",
                "@formatjs/intl-pluralrules/polyfill",
                "@formatjs/intl-pluralrules/locale-data/en",
            ]
        );
    }

    #[test]
    fn side_effect_mixed_with_regular() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import 'reflect-metadata';
            import { Injectable } from 'tsyringe';
            import './instrument';
            import { App } from './App';
        "#,
        );
        assert_eq!(
            imports,
            vec!["reflect-metadata", "tsyringe", "./instrument", "./App"]
        );
    }

    #[test]
    fn side_effect_not_extracted_from_comments() {
        let imports = extract_imports(
            "test.ts",
            r#"
            // import 'old-polyfill';
            /* import 'removed-shim'; */
            import 'actual-polyfill';
        "#,
        );
        assert_eq!(imports, vec!["actual-polyfill"]);
    }

    #[test]
    fn side_effect_not_extracted_from_strings() {
        let imports = extract_imports(
            "test.ts",
            r#"
            const code = "import 'fake-polyfill';";
            const tmpl = `import 'template-polyfill';`;
            import 'real-polyfill';
        "#,
        );
        assert_eq!(imports, vec!["real-polyfill"]);
    }

    #[test]
    fn side_effect_deduplicates() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import 'myorg/frontend/styles';
            import 'myorg/frontend/styles';
        "#,
        );
        assert_eq!(imports, vec!["myorg/frontend/styles"]);
    }

    #[test]
    fn scoped_packages() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import { useQuery } from '@tanstack/react-query';
            import { Button } from '@mui/material';
            import '@sentry/nextjs';
        "#,
        );
        assert_eq!(
            imports,
            vec!["@tanstack/react-query", "@mui/material", "@sentry/nextjs"]
        );
    }

    #[test]
    fn subpath_imports() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import debounce from 'lodash/debounce';
            import { DevTools } from '@tanstack/react-query/devtools';
        "#,
        );
        assert_eq!(
            imports,
            vec!["lodash/debounce", "@tanstack/react-query/devtools"]
        );
    }

    #[test]
    fn node_builtins() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import path from 'node:path';
            import { readFileSync } from 'fs';
            import { createServer } from 'node:http';
        "#,
        );
        assert_eq!(imports, vec!["node:path", "fs", "node:http"]);
    }

    #[test]
    fn multiline_imports() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import {
                useState,
                useEffect,
                useMemo,
            } from 'react';
            import type {
                User,
                Post,
            } from 'myorg/frontend/types';
        "#,
        );
        assert_eq!(imports, vec!["react", "myorg/frontend/types"]);
    }

    #[test]
    fn require_calls_extracted() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import { hydrateRoot } from 'react-dom';
            const React = require('react');
            require('side-effect');
            module.exports = require('@scope/pkg/subpath');
            exports.fp = require('lodash/fp');
        "#,
        );
        assert_eq!(
            imports,
            vec![
                "react-dom",
                "react",
                "side-effect",
                "@scope/pkg/subpath",
                "lodash/fp"
            ]
        );
    }

    #[test]
    fn dynamic_and_shadowed_require_not_extracted() {
        let imports = extract_imports(
            "test.ts",
            r#"
            const packageName = 'react';
            require(packageName);
            require(`side-effect`);
            require.resolve('resolve-only');

            function local(require: (path: string) => unknown) {
                require('shadowed');
            }
            const arrow = (require: (path: string) => unknown) => require('also-shadowed');
        "#,
        );
        assert_eq!(imports, Vec::<String>::new());
    }

    #[test]
    fn hashbang_imports() {
        let imports = extract_imports(
            "test.ts",
            r#"
            import { x } from '#myorg/frontend/common';
            import '#myorg/frontend/styles';
        "#,
        );
        assert_eq!(
            imports,
            vec!["#myorg/frontend/common", "#myorg/frontend/styles"]
        );
    }

    #[test]
    fn malformed_file_does_not_panic() {
        // oxc does error recovery — the key contract is no panics on malformed input
        let imports = extract_imports(
            "test.ts",
            r#"
            import { foo } from 'valid-before';
            const x = {{{;  // syntax error
            import { bar } from 'valid-after';
        "#,
        );
        // oxc may recover some or all imports depending on how the error recovery works.
        // The important thing is that it doesn't panic.
        let _ = imports;
    }

    #[test]
    fn completely_malformed_file() {
        // Should not panic, returns whatever oxc can recover (possibly empty)
        let imports = extract_imports("test.ts", "}{}{}{}{{{{}}}");
        // Just verify it doesn't panic — result may be empty
        let _ = imports;
    }

    #[test]
    fn empty_import_path() {
        // import from '' — oxc parses it, we filter empty strings
        let imports = extract_imports("test.ts", "import '' ;");
        assert!(imports.is_empty());
    }

    #[test]
    fn dynamic_import_in_function_body() {
        let imports = extract_imports(
            "test.ts",
            "import { foo } from 'static';\nasync function f() { await import('dynamic'); }",
        );
        assert!(
            imports.contains(&"dynamic".to_string()),
            "Missing dynamic import inside function body. Got: {:?}",
            imports
        );
    }

    #[test]
    fn inline_import_type_access() {
        // Pattern: import('postcss').Root — used for inline type annotations
        // e.g. in vite configs: Once(root: import('postcss').Root) { ... }
        let imports = extract_imports(
            "test.ts",
            r#"
            const plugin = {
                postcssPlugin: 'my-plugin',
                Once(root: import('postcss').Root) {
                    root.walkRules((rule: import('postcss').Rule) => {});
                },
            };
        "#,
        );
        assert!(
            imports.contains(&"postcss".to_string()),
            "inline import('postcss').Type not extracted. Got: {:?}",
            imports
        );
    }

    #[test]
    fn dynamic_import_with_generic_return_type() {
        let imports = extract_imports(
            "test.ts",
            "import { foo } from 'static';\nexport async function f(): Promise<string | undefined> { return import('dynamic'); }",
        );
        assert!(imports.contains(&"static".to_string()));
        assert!(imports.contains(&"dynamic".to_string()));
    }
}
