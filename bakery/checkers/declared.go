package checkers

import (
	"strings"

	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"
)

// Declared represents a value declared by virtue of being
// present as a first-party caveat.
type Declared struct {
	// Condition holds the resolved caveat condition.
	Condition string
	// Value holds the declared argument to the condition.
	Value string
}

// NeedDeclaredCaveat returns a third party caveat that
// wraps the provided third party caveat and requires
// that the third party must add "declared" caveats for
// all the named keys.
// TODO(rog) namespaces in third party caveats?
// TODO(rog) deprecate this in favour of in-built field in third party caveat format.
func NeedDeclaredCaveat(cav Caveat, keys ...string) Caveat {
	if cav.Location == "" {
		return ErrorCaveatf("need-declared caveat is not third-party")
	}
	return Caveat{
		Location:  cav.Location,
		Condition: CondNeedDeclared + " " + strings.Join(keys, ",") + " " + cav.Condition,
	}
}

type declaredKey string

// ContextWithDeclared returns a context with attached declared information,
// as returned from InferDeclared.
func ContextWithDeclared(ctxt context.Context, declared Declared) context.Context {
	return context.WithValue(ctxt, declaredKey(declared.Condition), declared.Value)
}

func declaredFromContext(ctxt context.Context, cond string) string {
	val, _ := ctxt.Value(declaredKey(cond)).(string)
	return val
}

// RegisterDeclaredCaveat registers a checker for the given declaration
// caveat in the given namespace URI. Caveats with the
// given condition will succeed if the context is associated with a
// Declared value that has the same condition as its argument.
// (see ContextWithDeclared).
func RegisterDeclaredCaveat(c *Checker, cond, uri string) {
	c.Register(cond, uri, checkDeclared)
}

func checkDeclared(ctx context.Context, cond, arg string) error {
	if val := declaredFromContext(ctx, cond); arg != val {
		return errgo.Newf("got %q, expected %q", arg, val)
	}
	return nil
}

// InferDeclared infers a declared value for the given caveat condition, which
// should be a resolved caveat condition including its prefix, but without
// any argument.
//
// The conditions are assumed to be first party caveat conditions.
// If the condition is not present, or is declared more than once with different values,
// the resulting Declared value will have an empty Value field.
func InferDeclared(declCond string, conds []string) Declared {
	val, found := "", false
	for _, cond := range conds {
		name, arg, _ := ParseCaveat(cond)
		switch {
		case name != declCond:
		case !found:
			val, found = arg, true
		case arg != val:
			val = ""
		}
	}
	return Declared{
		Condition: declCond,
		Value:     val,
	}
}
