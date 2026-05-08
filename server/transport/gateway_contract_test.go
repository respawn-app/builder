package transport

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"builder/shared/rpccontract"
)

func TestGatewayUnaryDispatchCoversRouteContract(t *testing.T) {
	dispatchMethods := gatewayDispatchCaseMethods(t)
	for _, route := range rpccontract.Routes() {
		if route.Kind != rpccontract.KindUnary {
			continue
		}
		if _, ok := dispatchMethods[route.Method]; !ok {
			t.Fatalf("unary route %q missing gateway dispatch case", route.Method)
		}
	}
	for method := range dispatchMethods {
		route, ok := rpccontract.RouteByMethod(method)
		if !ok {
			t.Fatalf("gateway dispatch case %q missing route contract", method)
		}
		if route.Kind != rpccontract.KindUnary {
			t.Fatalf("gateway dispatch case %q route kind = %q, want unary", method, route.Kind)
		}
	}
}

func gatewayDispatchCaseMethods(t *testing.T) map[string]struct{} {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	dir := filepath.Dir(filename)
	protocolMethods := gatewayProtocolMethodValues(t, dir)
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, "gateway.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse gateway.go: %v", err)
	}
	methods := map[string]struct{}{}
	for _, decl := range file.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok || funcDecl.Name.Name != "dispatch" || funcDecl.Body == nil {
			continue
		}
		ast.Inspect(funcDecl.Body, func(node ast.Node) bool {
			switchStmt, ok := node.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			for _, stmt := range switchStmt.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range clause.List {
					method, ok := gatewayProtocolMethodValue(protocolMethods, expr)
					if ok {
						methods[method] = struct{}{}
					}
				}
			}
			return true
		})
	}
	if len(methods) == 0 {
		t.Fatal("no gateway dispatch cases found")
	}
	return methods
}

func gatewayProtocolMethodValues(t *testing.T, transportDir string) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(transportDir, "..", "..", "shared", "protocol", "handshake.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse protocol methods: %v", err)
	}
	values := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range valueSpec.Names {
				if index >= len(valueSpec.Values) {
					continue
				}
				lit, ok := valueSpec.Values[index].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote method %s: %v", name.Name, err)
				}
				values[name.Name] = value
			}
		}
	}
	return values
}

func gatewayProtocolMethodValue(values map[string]string, expr ast.Expr) (string, bool) {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || pkg.Name != "protocol" {
		return "", false
	}
	value, ok := values[selector.Sel.Name]
	return value, ok
}
