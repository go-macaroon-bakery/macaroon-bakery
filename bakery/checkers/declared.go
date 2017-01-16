package checkers

import (
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

type declaredKey string

// ContextWithDeclared returns a context with attached declared information,
// as returned from InferDeclared.
func ContextWithDeclared(ctx context.Context, declared Declared) context.Context {
	return context.WithValue(ctx, declaredKey(declared.Condition), declared.Value)
}

func declaredFromContext(ctx context.Context, cond string) string {
	val, _ := ctx.Value(declaredKey(cond)).(string)
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
		name, arg, _ := ParseCondition(cond)
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
