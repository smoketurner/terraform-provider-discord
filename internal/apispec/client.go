package apispec

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// ClientCall is a request the Discord client makes, recovered from its
// source.
type ClientCall struct {
	// Func is the client method that makes the request.
	Func string
	// Method is the HTTP method.
	Method string
	// Path is the request path with every computed segment written as "{}".
	Path string
	// Query lists the query parameter names the path sets literally.
	Query []string
	Pos   token.Position
}

// ClientCalls parses the non-test Go files in dir and returns every request
// made with an http.Method* constant followed by a path argument, such as
// c.do(ctx, http.MethodGet, "/guilds/"+guildID, nil, &g). A path built
// across several statements is followed through local variables.
func ClientCalls(dir string) ([]ClientCall, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("listing client sources: %w", err)
	}
	fset := token.NewFileSet()
	var calls []ClientCall
	var errs []error
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parsing client sources: %w", err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			found, err := funcCalls(fset, fn)
			calls = append(calls, found...)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(calls) == 0 && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("no client requests found in %s", dir))
	}
	return calls, errors.Join(errs...)
}

func funcCalls(fset *token.FileSet, fn *ast.FuncDecl) ([]ClientCall, error) {
	locals := map[string]ast.Expr{}
	var calls []ClientCall
	var errs []error
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) == len(n.Rhs) {
				for i, lhs := range n.Lhs {
					id, ok := lhs.(*ast.Ident)
					if !ok {
						continue
					}
					rhs := n.Rhs[i]
					if prev, ok := locals[id.Name]; ok && n.Tok == token.ADD_ASSIGN {
						rhs = &ast.BinaryExpr{X: prev, Op: token.ADD, Y: rhs}
					}
					locals[id.Name] = rhs
				}
			}
		case *ast.CallExpr:
			for i, arg := range n.Args {
				method, ok := httpMethod(arg)
				if !ok || i+1 >= len(n.Args) {
					continue
				}
				pos := fset.Position(n.Pos())
				path, err := pathTemplate(n.Args[i+1], locals, 0)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %s: %w", pos, fn.Name.Name, err))
					break
				}
				call := ClientCall{Func: fn.Name.Name, Method: method, Path: path, Pos: pos}
				if p, q, ok := strings.Cut(path, "?"); ok {
					call.Path = p
					values, err := url.ParseQuery(q)
					if err != nil {
						errs = append(errs, fmt.Errorf("%s: %s: query %q: %w", pos, fn.Name.Name, q, err))
						break
					}
					for k := range values {
						call.Query = append(call.Query, k)
					}
					slices.Sort(call.Query)
				}
				calls = append(calls, call)
				break
			}
		}
		return true
	})
	return calls, errors.Join(errs...)
}

// httpMethod reports whether e is a net/http method constant such as
// http.MethodGet, and returns the method.
func httpMethod(e ast.Expr) (string, bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "http" || !strings.HasPrefix(sel.Sel.Name, "Method") {
		return "", false
	}
	return strings.ToUpper(strings.TrimPrefix(sel.Sel.Name, "Method")), true
}

// pathTemplate rebuilds a request path from a string expression: literals
// are kept, local variables are followed, and anything else (parameters,
// function calls such as url.PathEscape) becomes a "{}" placeholder.
func pathTemplate(e ast.Expr, locals map[string]ast.Expr, depth int) (string, error) {
	if depth > 16 {
		return "", errors.New("path expression is too deeply nested")
	}
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", fmt.Errorf("path literal %s is not a string", e.Value)
		}
		return strconv.Unquote(e.Value)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", fmt.Errorf("unsupported operator %s in path", e.Op)
		}
		left, err := pathTemplate(e.X, locals, depth+1)
		if err != nil {
			return "", err
		}
		right, err := pathTemplate(e.Y, locals, depth+1)
		if err != nil {
			return "", err
		}
		return left + right, nil
	case *ast.ParenExpr:
		return pathTemplate(e.X, locals, depth+1)
	case *ast.Ident:
		if def, ok := locals[e.Name]; ok {
			return pathTemplate(def, locals, depth+1)
		}
		return "{}", nil
	case *ast.CallExpr, *ast.SelectorExpr, *ast.IndexExpr:
		return "{}", nil
	default:
		return "", fmt.Errorf("unsupported path expression %T", e)
	}
}
