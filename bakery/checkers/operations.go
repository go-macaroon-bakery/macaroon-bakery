package checkers

import (
	"fmt"
	"strings"

	"golang.org/x/net/context"
	errgo "gopkg.in/errgo.v1"
)

type opKey struct{}

// ContextWithOperations returns a context which is associated with all the
// given operations. An allow caveat will succeed only if one of the allowed
// operations is in ops; a deny caveat will succeed only if none of the denied
// operations are in ops.
func ContextWithOperations(ctxt context.Context, ops ...string) context.Context {
	return context.WithValue(ctxt, opKey{}, ops)
}

func operationsFromContext(ctxt context.Context) []string {
	ops, _ := ctxt.Value(opKey{}).([]string)
	return ops
}

// AllowCaveat returns a caveat that will deny attempts to use the
// macaroon to perform any operation other than those listed. Operations
// must not contain a space.
func AllowCaveat(op ...string) Caveat {
	if len(op) == 0 {
		return ErrorCaveatf("no operations allowed")
	}
	return operationCaveat(CondAllow, op)
}

// DenyCaveat returns a caveat that will deny attempts to use the
// macaroon to perform any of the listed operations. Operations
// must not contain a space.
func DenyCaveat(op ...string) Caveat {
	return operationCaveat(CondDeny, op)
}

// operationCaveat is a helper for AllowCaveat and DenyCaveat. It checks
// that all operation names are valid before createing the caveat.
func operationCaveat(cond string, op []string) Caveat {
	for _, o := range op {
		if strings.IndexByte(o, ' ') != -1 {
			return ErrorCaveatf("invalid operation name %q", o)
		}
	}
	return firstParty(cond, strings.Join(op, " "))
}

func checkAllow(ctxt context.Context, _, arg string) error {
	return checkOperation(ctxt, true, arg)
}

func checkDeny(ctxt context.Context, _, arg string) error {
	return checkOperation(ctxt, false, arg)
}

// checkOperation checks an allow or a deny caveat. The needOps
// parameter specifies whether we require all the operations in the
// caveat to be declared in the context.
func checkOperation(ctxt context.Context, needOps bool, arg string) error {
	ctxtOps := operationsFromContext(ctxt)
	if len(ctxtOps) == 0 {
		if needOps {
			f := strings.Fields(arg)
			if len(f) == 0 {
				return errgo.New("no operations allowed")
			}
			return errgo.Newf("%s not allowed", f[0])
		}
		return nil
	}

	fields := strings.Fields(arg)
	for _, op := range ctxtOps {
		if err := checkOneOp(op, needOps, fields); err != nil {
			return errgo.Mask(err)
		}
	}
	return nil
}

func checkOneOp(ctxtOp string, needOp bool, fields []string) error {
	var found bool
	for _, op := range fields {
		if op == ctxtOp {
			found = true
			break
		}
	}
	if found != needOp {
		return fmt.Errorf("%s not allowed", ctxtOp)
	}
	return nil
}
