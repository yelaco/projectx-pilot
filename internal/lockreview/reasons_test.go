package lockreview

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// All fixed submission, risk and monitor lock triggers must have an intentional review policy. This
// catches newly added blockers that would otherwise have no recovery guidance.
func TestEveryAutomaticFaultTriggerHasAReviewClassification(t *testing.T) {
	count := 0
	for _, path := range []string{"../execution/monitor.go", "../execution/engine.go"} {
		tree, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range tree.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				var arg ast.Expr
				if (selector.Sel.Name == "requireAttention" || selector.Sel.Name == "requireAttentionWithKind" || selector.Sel.Name == "lock") && len(call.Args) > 1 {
					arg = call.Args[1]
				} else if selector.Sel.Name == "LockExecution" && len(call.Args) > 2 {
					arg = call.Args[2]
				} else if fn.Name.Name == "verifyProtection" && selector.Sel.Name == "New" && len(call.Args) > 0 {
					arg = call.Args[0]
				}
				literal, ok := arg.(*ast.BasicLit)
				if !ok {
					return true
				}
				reason, err := strconv.Unquote(literal.Value)
				if err != nil {
					return true
				}
				info := Classify(reason)
				if info.Category == "" || info.Category == "Unrecognized lock" {
					t.Errorf("unclassified lock trigger %q", reason)
				}
				count++
				return true
			})
		}

	}
	if count < 20 {
		t.Fatal("lock trigger audit did not cover protection and management paths", count)
	}
}
func TestRiskAndUnknownReasonsAreNotConfirmationOverrides(t *testing.T) {
	for _, reason := range []string{"daily loss limit reached", "session loss or trade limit reached", "liquidation buffer reached", "unknown new error", "protective orders missing, pending or inconsistent"} {
		if Classify(reason).Reviewable {
			t.Fatal("unsafe general override", reason)
		}
	}
}
