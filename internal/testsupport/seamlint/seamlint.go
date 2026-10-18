// Package seamlint is the mechanical check for the repository's testing-seam
// rule (AGENTS.md, "Testing seam (fixed decision — do not reopen per
// milestone)"): product behaviour is tested through the engine entry point —
// Run(ctx, Request) or New(Dependencies).Run — with the closed set of request
// types in and a report out, against the real Terraform binary.
//
// The check enforces the mechanically decidable parts of that rule and
// nothing more. A test file never constructs the engine's internal settings
// value; a test file outside internal/tfexec never constructs the Terraform
// runner itself; a test file outside the engine and outside a package's own
// tests never imports a package that only the engine drives. Everything else
// — how a test asserts, which lower-layer contracts deserve a direct test —
// stays with the recorded exceptions in AGENTS.md and with review.
//
// The rules live in one place, as a Go test in the standard suite, because
// the lint configuration cannot state them: forbidigo matches identifier
// mentions rather than constructions (it would flag the engine's own type
// positions and helper parameters), and depguard matches imports rather than
// symbols (the settings type is unexported, so no import rule can even see
// it).
package seamlint

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// modulePath is this repository's import path root.
	modulePath = "github.com/andrewesweet/tf-mut"
	// enginePackage is the application layer the seam guards.
	enginePackage = modulePath + "/internal/engine"
	// tfexecPackage owns the concrete Terraform runner.
	tfexecPackage = modulePath + "/internal/tfexec"

	// settingsTypeName is the engine's internal settings type. It is
	// unexported, so only the engine package's own files can name it — which
	// is exactly where the rule has to bite.
	settingsTypeName = "settings"
	// runnerTypeName is the concrete Terraform runner type.
	runnerTypeName = "Runner"

	// testFileSuffix selects the files the seam governs.
	testFileSuffix = "_test.go"
)

// Rule names the part of the testing-seam rule a violation breaks.
type Rule string

const (
	// RuleSettingsConstruction marks a test file constructing the engine's
	// internal settings value: tests construct requests, never settings. The
	// rule covers the composite literal, a declared variable of the type and
	// a new call; a bare mention in a parameter or field position constructs
	// nothing and stays allowed.
	RuleSettingsConstruction Rule = "settings-construction"
	// RuleRunnerConstruction marks a test file outside internal/tfexec
	// constructing the Terraform runner itself: the engine builds the one
	// runner a run gets, from the request, against the real binary. The
	// runner package's own tests construct it legitimately.
	RuleRunnerConstruction Rule = "runner-construction"
	// RuleEngineDrivenImport marks a test file outside the engine importing
	// a package that only the engine drives: the oracle the engine judges
	// with, the sandbox the engine stages in, and the store behind the
	// engine's persistence ports. A package's own tests stay allowed.
	RuleEngineDrivenImport Rule = "engine-driven-import"
)

// Violation is one place a test file reaches past the seam in a way the
// syntax can decide.
type Violation struct {
	// Position is the violation's file, line and column. The file is
	// relative to the scanned root, with forward slashes.
	Position token.Position
	// Rule names the seam part the file breaks.
	Rule Rule
	// Detail names the constructed type or the imported package.
	Detail string
}

// Exception is one reviewed file the seam rules flag that stays: a
// legitimate lower-layer contract, never a silenced breach. Every entry
// carries a one-line reason and must stay live — the suite fails when the
// scanner no longer flags its file, so the list cannot outlive review.
type Exception struct {
	// Path is the file's repository-relative path, with forward slashes.
	Path string
	// Rule is the rule the file would otherwise break.
	Rule Rule
	// Reason is the one-line reason the file stays.
	Reason string
}

// Exceptions returns the reviewed exception list. Amend it only in a
// reviewed change, with a one-line reason, never to silence a genuine seam
// breach.
func Exceptions() []Exception {
	return []Exception{
		{
			Path: "internal/engine/costmodel_integration_test.go",
			Rule: RuleRunnerConstruction,
			Reason: "the M2 cost-model measurement drives the real Terraform binary through the " +
				"concrete tfexec runner directly; network-gated research instrumentation, " +
				"not product behaviour",
		},
		{
			Path: "internal/store/verdict_test.go",
			Rule: RuleEngineDrivenImport,
			Reason: "the verdict cache's round-trip is a contract about serialized oracle value " +
				"types; it never judges a verdict",
		},
	}
}

// Scan returns every seam violation the rules find in the test files under
// root, before the reviewed exceptions are applied. A file that does not
// parse is an error: the check fails closed.
func Scan(root string) ([]Violation, error) {
	root = filepath.Clean(root)

	var found []Violation

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return skipDir(root, path, entry)
		}

		if !strings.HasSuffix(path, testFileSuffix) {
			return nil
		}

		violations, err := scanFile(root, path)
		if err != nil {
			return err
		}

		found = append(found, violations...)

		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("scanning %s: %w", root, walkErr)
	}

	slices.SortFunc(found, compareViolations)

	return found, nil
}

// Check scans the test files under root and returns the violations the
// reviewed exceptions do not cover. Empty means the tree holds the seam.
func Check(root string) ([]Violation, error) {
	found, err := Scan(root)
	if err != nil {
		return nil, err
	}

	remaining := make([]Violation, 0, len(found))
	for _, violation := range found {
		if !excepted(violation) {
			remaining = append(remaining, violation)
		}
	}

	return remaining, nil
}

// excepted reports whether the reviewed exception list covers the violation.
func excepted(violation Violation) bool {
	return slices.ContainsFunc(Exceptions(), func(exception Exception) bool {
		return exception.Path == violation.Position.Filename && exception.Rule == violation.Rule
	})
}

// skipDir prunes everything that is not this module's own suite: fixture
// directories, dot directories and nested modules such as the pinned tool
// builds under tools.
func skipDir(root, path string, entry fs.DirEntry) error {
	if path == root {
		return nil
	}

	name := entry.Name()
	if name == "testdata" || strings.HasPrefix(name, ".") {
		return filepath.SkipDir
	}

	if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
		return filepath.SkipDir
	}

	return nil
}

// scanner carries one parsed test file through the three rules.
type scanner struct {
	root       string
	path       string
	file       *ast.File
	fset       *token.FileSet
	relative   string
	aliases    map[string]string // local import name -> import path
	dotImports map[string]bool   // import paths dot-imported into the file
	violations []Violation
}

// scanFile parses one test file and runs every rule over it.
func scanFile(root, path string) ([]Violation, error) {
	fset := token.NewFileSet()

	parsed, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	relative := path
	if rel, err := filepath.Rel(root, path); err == nil {
		relative = rel
	}

	s := &scanner{
		root:       root,
		path:       path,
		file:       parsed,
		fset:       fset,
		relative:   filepath.ToSlash(relative),
		aliases:    make(map[string]string),
		dotImports: make(map[string]bool),
	}

	s.collectImports()
	s.inspect()

	return s.violations, nil
}

// collectImports records each import's local name, and any dot import.
func (s *scanner) collectImports() {
	for _, spec := range s.file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)

		switch {
		case spec.Name == nil:
			s.aliases[filepath.Base(importPath)] = importPath
		case spec.Name.Name == ".":
			s.dotImports[importPath] = true
		default:
			s.aliases[spec.Name.Name] = importPath
		}
	}
}

// inspect walks the file and checks every node the rules read.
func (s *scanner) inspect() {
	ast.Inspect(s.file, func(node ast.Node) bool {
		switch marked := node.(type) {
		case *ast.ImportSpec:
			s.checkImport(marked)
		case *ast.CompositeLit:
			s.checkConstructed(marked.Type)
		case *ast.ValueSpec:
			s.checkConstructed(marked.Type)
		case *ast.CallExpr:
			s.checkNew(marked)
		default:
			// Nodes the rules do not read.
		}

		return true
	})
}

// checkImport applies the engine-driven import rule.
func (s *scanner) checkImport(spec *ast.ImportSpec) {
	importPath := strings.Trim(spec.Path.Value, `"`)
	if !engineDriven(importPath) || s.ownedByOwnPackage(importPath) {
		return
	}

	s.report(spec.Pos(), RuleEngineDrivenImport, importPath)
}

// checkConstructed applies the two construction rules to one type position.
func (s *scanner) checkConstructed(typeExpr ast.Expr) {
	importPath, typeName, ok := s.resolve(typeExpr)
	if !ok {
		return
	}

	switch {
	case importPath == enginePackage && typeName == settingsTypeName:
		s.report(typeExpr.Pos(), RuleSettingsConstruction, shortName(importPath, typeName))
	case importPath == tfexecPackage && typeName == runnerTypeName && !s.ownsRunner():
		s.report(typeExpr.Pos(), RuleRunnerConstruction, shortName(importPath, typeName))
	default:
		// A type the rules do not govern: requests, report values, the rest.
	}
}

// checkNew applies both construction rules to a new call's operand.
func (s *scanner) checkNew(call *ast.CallExpr) {
	name, isIdent := call.Fun.(*ast.Ident)
	if !isIdent || name.Name != "new" || len(call.Args) != 1 {
		return
	}

	s.checkConstructed(call.Args[0])
}

// resolve names the package and type one type expression refers to, as far
// as the file's own imports can say. A bare identifier resolves to the
// file's own package, or to a dot import; a selector resolves through the
// file's import aliases.
func (s *scanner) resolve(typeExpr ast.Expr) (importPath, typeName string, ok bool) {
	switch marked := typeExpr.(type) {
	case *ast.Ident:
		if path, isOwn := ownPackagePath(s.file.Name.Name); isOwn {
			return path, marked.Name, true
		}

		if s.dotImports[tfexecPackage] && marked.Name == runnerTypeName {
			return tfexecPackage, runnerTypeName, true
		}

		return "", "", false
	case *ast.SelectorExpr:
		name, isIdent := marked.X.(*ast.Ident)
		if !isIdent {
			return "", "", false
		}

		if path, isAlias := s.aliases[name.Name]; isAlias {
			return path, marked.Sel.Name, true
		}

		return "", "", false
	default:
		return "", "", false
	}
}

// engineDriven reports whether a package's entire purpose is to be driven by
// the engine, so that a test outside the engine and outside the package's
// own tests can only reach it past the seam.
func engineDriven(importPath string) bool {
	switch importPath {
	case modulePath + "/internal/oracle", modulePath + "/internal/sandbox", modulePath + "/internal/store":
		return true
	default:
		return false
	}
}

// ownedByOwnPackage reports whether the file lives beside the imported
// package: a package's own tests are the lower-layer contracts the seam
// deliberately allows, and the engine's tests are the projection tests the
// recorded exceptions describe.
func (s *scanner) ownedByOwnPackage(importPath string) bool {
	owned := strings.TrimPrefix(importPath, modulePath+"/")

	dir := s.directory()

	return dir == owned || dir == "internal/engine"
}

// ownsRunner reports whether the file lives in the runner package's own
// directory, whose tests construct the runner legitimately.
func (s *scanner) ownsRunner() bool {
	return s.directory() == "internal/tfexec"
}

// directory is the file's directory, relative to the scanned root, with
// forward slashes.
func (s *scanner) directory() string {
	return filepath.ToSlash(filepath.Dir(s.relative))
}

// ownPackagePath maps a file's own package name to its import path, for bare
// identifiers. A same-package test file cannot shadow these names, so the
// bare identifier is always the type.
func ownPackagePath(packageName string) (string, bool) {
	switch packageName {
	case "engine":
		return enginePackage, true
	case "tfexec":
		return tfexecPackage, true
	default:
		return "", false
	}
}

// shortName renders a resolved type as package.type for the violation text.
func shortName(importPath, typeName string) string {
	return filepath.Base(importPath) + "." + typeName
}

// report records one violation with its root-relative position.
func (s *scanner) report(pos token.Pos, rule Rule, detail string) {
	position := s.fset.Position(pos)
	position.Filename = s.relative

	s.violations = append(s.violations, Violation{
		Position: position,
		Rule:     rule,
		Detail:   detail,
	})
}

// compareViolations orders violations by position, so scans are stable
// whatever order the walk visits files in.
func compareViolations(first, second Violation) int {
	return cmp.Or(
		strings.Compare(first.Position.Filename, second.Position.Filename),
		cmp.Compare(first.Position.Line, second.Position.Line),
		cmp.Compare(first.Position.Column, second.Position.Column),
	)
}
