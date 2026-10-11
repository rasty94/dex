package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	api "github.com/dexidp/dex/api/v2"
	"github.com/dexidp/dex/storage"
)

func TestGRPCCode(t *testing.T) {
	_, b64Err := base64.RawURLEncoding.DecodeString("not base64!")
	protoErr := proto.Unmarshal([]byte{0xff, 0xff}, &api.Client{})

	for _, tc := range []struct {
		name string
		err  error
		want codes.Code
	}{
		{"storage not found", storage.ErrNotFound, codes.NotFound},
		{"wrapped already exists", fmt.Errorf("create: %w", storage.ErrAlreadyExists), codes.AlreadyExists},
		{"not found flattened by %v", fmt.Errorf("get auth session: %v", storage.ErrNotFound), codes.NotFound},
		{"subject not base64", b64Err, codes.InvalidArgument},
		{"subject not protobuf", protoErr, codes.InvalidArgument},
		{"missing field", errors.New("no email supplied"), codes.InvalidArgument},
		{"static object", fmt.Errorf("update password: %v", errors.New("static passwords: read-only cannot update password")), codes.FailedPrecondition},
		{"feature flag", errors.New("api_connectors_crud feature flag is not enabled"), codes.Unimplemented},
		{"storage failure", fmt.Errorf("create client: %v", errors.New("database is locked")), codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := grpcCode(tc.err); got != tc.want {
				t.Errorf("grpcCode(%q) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestErrorCodeInterceptor(t *testing.T) {
	intercept := newErrorCodeInterceptor()
	call := func(err error) error {
		_, got := intercept(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/api.Dex/CreatePassword"},
			func(context.Context, interface{}) (interface{}, error) { return nil, err })
		return got
	}

	if err := call(nil); err != nil {
		t.Fatalf("a successful call came back as %v", err)
	}

	got := status.Convert(call(errors.New("no email supplied")))
	if got.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got.Code())
	}
	// The dashboard and other callers match on the text; it must not change.
	if got.Message() != "no email supplied" {
		t.Errorf("message = %q, want it unchanged", got.Message())
	}

	// A code set upstream of the handler (the auth interceptor's) is kept.
	if c := status.Code(call(status.Error(codes.Unauthenticated, "invalid authorization token"))); c != codes.Unauthenticated {
		t.Errorf("an explicit code was replaced by %v", c)
	}
}

// The classifier reads messages, and the messages live in upstream's files. A
// validation error that no rule recognizes would be reported as Internal: the
// caller would blame dex for its own bad request. So every message the
// handlers build without wrapping an error must classify as something else.
// When this fails after a merge, upstream added a message; add a rule for it.
func TestEveryAPIServerMessageIsClassified(t *testing.T) {
	// Sentinels the handlers consume themselves and never return.
	notReturned := map[string]string{
		"identity unchanged": "mfa.go turns it into NotFound: true",
	}

	files, err := filepath.Glob("../../server/apiserver/*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var checked int
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			msg, ok := constructedMessage(n)
			if !ok {
				return true
			}
			checked++
			if _, internal := notReturned[msg]; internal {
				return true
			}
			if c := grpcCode(errors.New(msg)); c == codes.Internal {
				t.Errorf("%s: %q would reach callers as Internal", fset.Position(n.Pos()), msg)
			}
			return true
		})
	}
	// Guards the walk itself: finding nothing would pass without checking.
	if checked < 40 {
		t.Fatalf("checked only %d messages; the source walk is broken", checked)
	}
}

// constructedMessage returns the message of an errors.New or fmt.Errorf call
// that builds an error from scratch, with any verbs filled in. A call that
// wraps another error (an argument named err) is a pass-through failure and
// is meant to be Internal, so it is skipped.
func constructedMessage(n ast.Node) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || !(pkg.Name == "errors" && sel.Sel.Name == "New" || pkg.Name == "fmt" && sel.Sel.Name == "Errorf") {
		return "", false
	}
	format, ok := stringLiteral(call.Args[0])
	if !ok {
		return "", false
	}
	var args []any
	for _, a := range call.Args[1:] {
		if id, ok := a.(*ast.Ident); ok && id.Name == "err" {
			return "", false
		}
		args = append(args, "x")
	}
	return fmt.Sprintf(format, args...), true
}

// stringLiteral reads a literal or a concatenation of literals.
func stringLiteral(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		l, ok1 := stringLiteral(e.X)
		r, ok2 := stringLiteral(e.Y)
		return l + r, ok1 && ok2
	}
	return "", false
}
