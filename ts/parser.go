package ts

// ImportStatement is a single import found in a TypeScript file. We track the
// source file so error messages can point at the exact file that introduced a
// particular dependency.
type ImportStatement struct {
	ImportPath string // module specifier (e.g., "react", "#packages/foo/x.js")
	SourceFile string // file containing the import (e.g., "packages/foo/src/index.ts")
}

// GlobalReference is a referenced global symbol found in TypeScript code. A
// directive can map these to ambient type packages.
type GlobalReference struct {
	Name       string // global name (e.g., "process", "chrome", "google.accounts")
	SourceFile string // file containing the reference
}

type ExtractedReferences struct {
	Imports []ImportStatement
	Globals []GlobalReference
	HasMain bool
}

// extractImportsBatch sends a batch of file paths through the cgo FFI and
// returns the parsed imports keyed by file path. Per-call batching keeps
// Rust's rayon parallelism alive across all files in the batch.
func (l *tsLang) extractImportsBatch(filePaths []string) (map[string]ExtractedReferences, error) {
	result, err := extractImports(filePaths)
	if err != nil {
		return nil, err
	}

	refs := make(map[string]ExtractedReferences, len(result))
	for file, extracted := range result {
		fileRefs := ExtractedReferences{
			Imports: make([]ImportStatement, 0, len(extracted.ImportPaths)),
			Globals: make([]GlobalReference, 0, len(extracted.GlobalNames)),
			HasMain: extracted.HasMain,
		}
		for _, p := range extracted.ImportPaths {
			fileRefs.Imports = append(fileRefs.Imports, ImportStatement{
				ImportPath: p,
				SourceFile: file,
			})
		}
		for _, g := range extracted.GlobalNames {
			fileRefs.Globals = append(fileRefs.Globals, GlobalReference{
				Name:       g,
				SourceFile: file,
			})
		}
		refs[file] = fileRefs
	}
	return refs, nil
}
