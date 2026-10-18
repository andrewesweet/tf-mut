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
	// a package that only the engine drives. A package's own tests stay allowed.
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
			Path:   "internal/characterise/functions_test.go",
			Rule:   RuleEngineDrivenImport,
			Reason: "AGENTS.md exception 5: discovery validation expressions exercise the static function table, not engine verdicts",
		},
		{
			Path:   "internal/fingerprint/fingerprint_test.go",
			Rule:   RuleEngineDrivenImport,
			Reason: "AGENTS.md exception 1: tfexec payload documents exercise canonicalisation shapes, not Terraform execution",
		},
		{
			Path:   "internal/oracle/metrics_test.go",
			Rule:   RuleEngineDrivenImport,
			Reason: "mutation operator identities label the oracle population arithmetic contract; no mutants are generated",
		},
		{
			Path:   "internal/suggest/adapters_test.go",
			Rule:   RuleEngineDrivenImport,
			Reason: "AGENTS.md exception 4: discovery, fingerprint and tfexec values supply the three document adapter matrices",
		},
		{
			Path:   "internal/suggest/evidence_contract_test.go",
			Rule:   RuleEngineDrivenImport,
			Reason: "AGENTS.md exception 9: fingerprint changes supply document evidence for the suggestion contract",
		},
		{
			Path:   "internal/suggest/schema_wire_test.go",
			Rule:   RuleEngineDrivenImport,
			Reason: "AGENTS.md exception 9: recorded tfexec schema documents exercise the suggestion rendering contract",
		},
		{
			Path:   "cmd/tf-mut/main_test.go",
			Rule:   RuleEngineDrivenImport,
			Reason: "the tracing environment name makes CLI tests hermetic; no tracer or product behaviour is driven directly",
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
	var files []*scanner
	packages := make(map[string]map[string]aliasDeclaration)

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return skipDir(root, path, entry)
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parseFile(root, path)
		if err != nil {
			return err
		}

		key := filepath.Dir(path) + "/" + file.file.Name.Name
		if packages[key] == nil {
			packages[key] = make(map[string]aliasDeclaration)
		}
		file.typeAliases = packages[key]
		file.collectTypeAliases()
		files = append(files, file)

		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("scanning %s: %w", root, walkErr)
	}

	for _, file := range files {
		if strings.HasSuffix(file.path, testFileSuffix) {
			file.inspect()
			found = append(found, file.violations...)
		}
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
	root        string
	path        string
	file        *ast.File
	fset        *token.FileSet
	relative    string
	aliases     map[string]string // local import name -> import path
	dotImports  map[string]bool   // import paths dot-imported into the file
	violations  []Violation
	typeAliases map[string]aliasDeclaration
}

type aliasDeclaration struct {
	expr ast.Expr
	file *scanner
}

func parseFile(root, path string) (*scanner, error) {
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

	return s, nil
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

func (s *scanner) collectTypeAliases() {
	for _, declaration := range s.file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.TYPE {
			continue
		}

		for _, spec := range group.Specs {
			alias, ok := spec.(*ast.TypeSpec)
			if ok && alias.Assign.IsValid() {
				s.typeAliases[alias.Name.Name] = aliasDeclaration{expr: alias.Type, file: s}
			}
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
			s.checkAllocation(marked)
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
	s.walkTypes(typeExpr, make(map[ast.Expr]bool), func(importPath, typeName string) {
		switch {
		case importPath == enginePackage && typeName == settingsTypeName:
			s.report(typeExpr.Pos(), RuleSettingsConstruction, shortName(importPath, typeName))
		case importPath == tfexecPackage && typeName == runnerTypeName && !s.ownsRunner():
			s.report(typeExpr.Pos(), RuleRunnerConstruction, shortName(importPath, typeName))
		}
	})
}

// checkAllocation applies both construction rules to an allocation's type.
func (s *scanner) checkAllocation(call *ast.CallExpr) {
	name, isIdent := call.Fun.(*ast.Ident)
	if !isIdent || (name.Name != "new" && name.Name != "make") || len(call.Args) < 1 {
		return
	}

	s.checkConstructed(call.Args[0])
}

func (s *scanner) walkTypes(typeExpr ast.Expr, seen map[ast.Expr]bool, visit func(string, string)) {
	if typeExpr == nil || seen[typeExpr] {
		return
	}
	seen[typeExpr] = true

	switch marked := typeExpr.(type) {
	case *ast.ArrayType:
		s.walkTypes(marked.Elt, seen, visit)
	case *ast.MapType:
		s.walkTypes(marked.Key, seen, visit)
		s.walkTypes(marked.Value, seen, visit)
	case *ast.StarExpr:
		s.walkTypes(marked.X, seen, visit)
	case *ast.ParenExpr:
		s.walkTypes(marked.X, seen, visit)
	case *ast.StructType:
		for _, field := range marked.Fields.List {
			s.walkTypes(field.Type, seen, visit)
		}
	case *ast.Ident:
		s.walkIdentifier(marked, seen, visit)
	case *ast.SelectorExpr:
		name, isIdent := marked.X.(*ast.Ident)
		if isIdent {
			if path, isAlias := s.aliases[name.Name]; isAlias {
				visit(path, marked.Sel.Name)
			}
		}
	}
}

func (s *scanner) walkIdentifier(name *ast.Ident, seen map[ast.Expr]bool, visit func(string, string)) {
	if name.Obj != nil {
		if alias, ok := name.Obj.Decl.(*ast.TypeSpec); ok && alias.Assign.IsValid() {
			s.walkTypes(alias.Type, seen, visit)
			return
		}
	} else if alias, ok := s.typeAliases[name.Name]; ok {
		alias.file.walkTypes(alias.expr, seen, visit)
		return
	}

	if s.dotImports[tfexecPackage] && name.Name == runnerTypeName {
		visit(tfexecPackage, runnerTypeName)
		return
	}

	if path, isOwn := ownPackagePath(s.file.Name.Name); isOwn {
		visit(path, name.Name)
	}
}

// engineDriven reports whether a package's entire purpose is to be driven by
// the engine, so that a test outside the engine and outside the package's
// own tests can only reach it past the seam.
func engineDriven(importPath string) bool {
	switch importPath {
	case modulePath + "/internal/discovery", modulePath + "/internal/mutation",
		modulePath + "/internal/fingerprint", modulePath + "/internal/oracle",
		modulePath + "/internal/suggest", modulePath + "/internal/characterise",
		modulePath + "/internal/sandbox", modulePath + "/internal/store",
		modulePath + "/internal/config", modulePath + "/internal/tracing", tfexecPackage:
		return true
	default:
		return false
	}
}

// ownedByOwnPackage reports whether the file lives beside the imported
// package. Under AGENTS.md's testing seam, engine tests and each package's
// own tests (including tfexec) are outside this import check by design;
// cross-package access requires a reviewed exception.
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
