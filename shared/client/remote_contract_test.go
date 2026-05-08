package client

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

type remoteRouteCall struct {
	connection rpccontract.ConnectionStrategy
	requestID  string
}

func TestRemoteClientRoutesMatchRPCContractConnectionStrategy(t *testing.T) {
	calls := remoteRouteCalls(t)
	for _, route := range rpccontract.Routes() {
		if route.Kind == rpccontract.KindNotification || route.Dependency == rpccontract.DependencyProtocol {
			continue
		}
		call, ok := calls[route.Method]
		if !ok {
			t.Fatalf("remote client missing binding for route %q", route.Method)
		}
		if call.connection != route.Connection {
			t.Fatalf("remote route %q connection = %q, want %q", route.Method, call.connection, route.Connection)
		}
		if route.Connection == rpccontract.ConnectionDedicated && call.requestID != route.DedicatedRequestID {
			t.Fatalf("remote route %q dedicated request id = %q, want %q", route.Method, call.requestID, route.DedicatedRequestID)
		}
	}
}

func remoteRouteCalls(t *testing.T) map[string]remoteRouteCall {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	dir := filepath.Dir(filename)
	methodValues := protocolMethodValues(t, dir)
	calls := map[string]remoteRouteCall{}
	for _, name := range []string{"remote.go", "remote_stream.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				receiver, _ := selector.X.(*ast.Ident)
				if receiver == nil || receiver.Name != "c" {
					return true
				}
				switch selector.Sel.Name {
				case "call":
					recordRemoteCall(calls, methodValues, call.Args, 1, "", rpccontract.ConnectionControl)
				case "callUnscoped":
					recordRemoteCall(calls, methodValues, call.Args, 1, "", rpccontract.ConnectionUnscoped)
				case "callDedicated":
					recordRemoteCall(calls, methodValues, call.Args, 2, stringArg(call.Args, 1), rpccontract.ConnectionDedicated)
				}
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "callRPC" {
				recordRemoteCall(calls, methodValues, call.Args, 3, stringArg(call.Args, 2), rpccontract.ConnectionSubscription)
			}
			return true
		})
		if name != "remote_stream.go" {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "Method" {
					continue
				}
				method, ok := methodValue(methodValues, kv.Value)
				if ok {
					calls[method] = remoteRouteCall{connection: rpccontract.ConnectionProgress}
				}
			}
			return true
		})
	}
	return calls
}

func recordRemoteCall(calls map[string]remoteRouteCall, methodValues map[string]string, args []ast.Expr, methodIndex int, requestID string, connection rpccontract.ConnectionStrategy) {
	method, ok := methodArg(methodValues, args, methodIndex)
	if !ok {
		return
	}
	calls[method] = remoteRouteCall{connection: connection, requestID: requestID}
}

func methodArg(methodValues map[string]string, args []ast.Expr, index int) (string, bool) {
	if index < 0 || index >= len(args) {
		return "", false
	}
	return methodValue(methodValues, args[index])
}

func methodValue(methodValues map[string]string, expr ast.Expr) (string, bool) {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || pkg.Name != "protocol" {
		return "", false
	}
	value, ok := methodValues[selector.Sel.Name]
	return value, ok
}

func stringArg(args []ast.Expr, index int) string {
	if index < 0 || index >= len(args) {
		return ""
	}
	lit, ok := args[index].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return value
}

func protocolMethodValues(t *testing.T, clientDir string) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(clientDir, "..", "protocol", "handshake.go"), nil, 0)
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
