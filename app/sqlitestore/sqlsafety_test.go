package sqlitestore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"
)

// SQL injection guard. Every query in this package passes values through
// "?" placeholders, so SQL text never contains request data. These tests
// keep it that way: they parse the package source and fail if the SQL
// argument of any database call could be anything but constant text.
//
// Constant means a string literal, a "+" of constants, a package const, a
// local variable only ever assigned constants (the ListTokens builder), or a
// parameter every caller passes a constant to (scanTokenList). The two
// deliberate exceptions are listed in sqlAllowlist. Prepared statements
// (receivers named *stmt*) take values, not SQL, and are skipped.

// sqlAllowlist maps "function: expression" to why it is safe.
var sqlAllowlist = map[string]string{
	"migrate: string(b)": "embedded migration files (migrationsFS), never request data",
	`migrate: fmt.Sprintf("PRAGMA user_version = %d", m.version)`: "an int from the hard-coded migration table",
}

// sqlArgIndex is the position of the SQL text for each database method.
var sqlArgIndex = map[string]int{
	"ExecContext": 1, "QueryContext": 1, "QueryRowContext": 1, "PrepareContext": 1,
	"Exec": 0, "Query": 0, "QueryRow": 0, "Prepare": 0,
}

type sqlChecker struct {
	files []*ast.File
	funcs map[string][]*ast.FuncDecl
}

type sqlCall struct {
	call *ast.CallExpr
	fn   *ast.FuncDecl
}

func newSQLChecker(files []*ast.File) *sqlChecker {
	c := &sqlChecker{files: files, funcs: map[string][]*ast.FuncDecl{}}
	for _, f := range files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				c.funcs[fn.Name.Name] = append(c.funcs[fn.Name.Name], fn)
			}
		}
	}
	return c
}

// check returns one message per database call whose SQL isn't constant,
// and how many database calls it examined.
func (c *sqlChecker) check(fset *token.FileSet) (violations []string, checked int) {
	for _, fns := range c.funcs {
		for _, fn := range fns {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				idx, ok := sqlArgIndex[sel.Sel.Name]
				if !ok || strings.Contains(strings.ToLower(types.ExprString(sel.X)), "stmt") || idx >= len(call.Args) {
					return true
				}
				checked++
				arg := call.Args[idx]
				if _, allowed := sqlAllowlist[fn.Name.Name+": "+types.ExprString(arg)]; allowed {
					return true
				}
				if !c.isConst(arg, fn, 0) {
					violations = append(violations, fset.Position(call.Pos()).String()+": "+fn.Name.Name+
						" passes non-constant SQL to "+sel.Sel.Name+": "+types.ExprString(arg))
				}
				return true
			})
		}
	}
	return violations, checked
}

func (c *sqlChecker) isConst(expr ast.Expr, fn *ast.FuncDecl, depth int) bool {
	if depth > 8 {
		return false
	}
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Kind == token.STRING
	case *ast.ParenExpr:
		return c.isConst(e.X, fn, depth+1)
	case *ast.BinaryExpr:
		return e.Op == token.ADD && c.isConst(e.X, fn, depth+1) && c.isConst(e.Y, fn, depth+1)
	case *ast.Ident:
		if e.Obj != nil && e.Obj.Kind == ast.Con {
			return true
		}
		if idx := paramIndex(fn, e.Name); idx >= 0 {
			return c.callersPassConst(fn.Name.Name, idx, depth+1)
		}
		return c.onlyAssignedConst(e.Name, fn, depth+1)
	}
	return false
}

// onlyAssignedConst reports whether name is assigned at least once in fn,
// and only ever from constants (=, :=, +=, or var name = …).
func (c *sqlChecker) onlyAssignedConst(name string, fn *ast.FuncDecl, depth int) bool {
	found, ok := false, true
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				if id, isIdent := lhs.(*ast.Ident); isIdent && id.Name == name {
					found = true
					if len(s.Rhs) != len(s.Lhs) || !c.isConst(s.Rhs[i], fn, depth) {
						ok = false
					}
				}
			}
		case *ast.ValueSpec:
			for i, id := range s.Names {
				if id.Name == name {
					found = true
					if i >= len(s.Values) || !c.isConst(s.Values[i], fn, depth) {
						ok = false
					}
				}
			}
		}
		return true
	})
	return found && ok
}

// callersPassConst reports whether every call to function name, anywhere in
// the package, passes a constant as argument idx.
func (c *sqlChecker) callersPassConst(name string, idx, depth int) bool {
	var calls []sqlCall
	for _, fns := range c.funcs {
		for _, fn := range fns {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch f := call.Fun.(type) {
				case *ast.Ident:
					if f.Name == name {
						calls = append(calls, sqlCall{call, fn})
					}
				case *ast.SelectorExpr:
					if f.Sel.Name == name {
						calls = append(calls, sqlCall{call, fn})
					}
				}
				return true
			})
		}
	}
	if len(calls) == 0 {
		return false
	}
	for _, sc := range calls {
		if idx >= len(sc.call.Args) || !c.isConst(sc.call.Args[idx], sc.fn, depth) {
			return false
		}
	}
	return true
}

func paramIndex(fn *ast.FuncDecl, name string) int {
	i := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			i++
			continue
		}
		for _, id := range field.Names {
			if id.Name == name {
				return i
			}
			i++
		}
	}
	return -1
}

func parseSources(t *testing.T, fset *token.FileSet, sources map[string]string) []*ast.File {
	t.Helper()
	var files []*ast.File
	for name, src := range sources {
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return files
}

func TestSQLTextIsAlwaysConstant(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	violations, checked := newSQLChecker(files).check(fset)
	for _, v := range violations {
		t.Error(v + " — pass values as ? arguments; SQL text must be constant")
	}
	// Guard the guard: if it stops recognising the database calls, it would
	// pass vacuously.
	if checked < 20 {
		t.Errorf("examined only %d database calls; the checker no longer recognises them", checked)
	}
}

func TestSQLCheckerCatchesBuiltQueries(t *testing.T) {
	const header = "package p\n\nimport (\"context\"; \"database/sql\"; \"fmt\")\n\n"
	for name, tc := range map[string]struct {
		src  string
		want int
	}{
		"literal and placeholders": {`func f(ctx context.Context, db *sql.DB, id string) { db.QueryRowContext(ctx, "SELECT 1 FROM t WHERE id = ?", id) }`, 0},
		"constant builder": {`func f(ctx context.Context, db *sql.DB, k string) {
			q := "SELECT 1 FROM t"
			if k != "" { q += " WHERE k = ?" }
			db.QueryContext(ctx, q, k)
		}`, 0},
		"prepared statement values": {`func f(ctx context.Context, stmt *sql.Stmt, v string) { stmt.ExecContext(ctx, v) }`, 0},
		"sprintf with a value":      {`func f(ctx context.Context, db *sql.DB, id string) { db.QueryContext(ctx, fmt.Sprintf("SELECT 1 FROM t WHERE id = '%s'", id)) }`, 1},
		"concatenated value":        {`func f(ctx context.Context, db *sql.DB, id string) { db.ExecContext(ctx, "DELETE FROM t WHERE id = '"+id+"'") }`, 1},
		"builder that appends a value": {`func f(ctx context.Context, db *sql.DB, col string) {
			q := "SELECT 1 FROM t ORDER BY "
			q += col
			db.QueryContext(ctx, q)
		}`, 1},
		"parameter from a caller's value": {`func run(ctx context.Context, db *sql.DB, q string) { db.QueryContext(ctx, q) }
			func f(ctx context.Context, db *sql.DB, name string) { run(ctx, db, "SELECT "+name) }`, 1},
	} {
		fset := token.NewFileSet()
		files := parseSources(t, fset, map[string]string{"p.go": header + tc.src})
		violations, _ := newSQLChecker(files).check(fset)
		if len(violations) != tc.want {
			t.Errorf("%s: %d violations, want %d: %v", name, len(violations), tc.want, violations)
		}
	}
}
