package identchecker

import (
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

// IdentityClient represents an abstract identity manager. User
// identities can be based on local informaton (for example
// HTTP basic auth) or by reference to an external trusted
// third party (an identity manager).
type IdentityClient interface {
	// IdentityFromContext returns the identity based on information in the context.
	// If it cannot determine the identity based on the context, then it
	// should return a set of caveats containing a third party caveat that,
	// when discharged, can be used to obtain the identity with DeclaredIdentity.
	//
	// It should only return an error if it cannot check the identity
	// (for example because of a database access error) - it's
	// OK to return all zero values when there's
	// no identity found and no third party to address caveats to.
	IdentityFromContext(ctx context.Context) (Identity, []checkers.Caveat, error)

	// DeclaredIdentity parses the identity declaration from the given
	// declared attributes.
	// TODO take the set of first party caveat conditions instead?
	DeclaredIdentity(ctx context.Context, declared map[string]string) (Identity, error)
}

// Identity holds identity information declared in a first party caveat
// added when discharging a third party caveat.
type Identity interface {
	// Id returns the id of the user, which may be an
	// opaque blob with no human meaning.
	// An id is only considered to be unique
	// with a given domain.
	Id() string

	// Domain holds the domain of the user. This
	// will be empty if the user was authenticated
	// directly with the identity provider.
	Domain() string
}

// noIdentities defines the null identity provider - it never returns any identities.
type noIdentities struct{}

// IdentityFromContext implements IdentityClient.IdentityFromContext by
// never returning a declared identity or any caveats.
func (noIdentities) IdentityFromContext(ctx context.Context) (Identity, []checkers.Caveat, error) {
	return nil, nil, nil
}

// DeclaredIdentity implements IdentityClient.DeclaredIdentity by
// always returning an error.
func (noIdentities) DeclaredIdentity(ctx context.Context, declared map[string]string) (Identity, error) {
	return nil, errgo.Newf("no identity declared or possible")
}

var _ ACLIdentity = SimpleIdentity("")

// SimpleIdentity implements a simple form of identity where
// the user is represented by a string.
type SimpleIdentity string

// Domain implements Identity.Domain by always
// returning the empty domain.
func (SimpleIdentity) Domain() string {
	return ""
}

// Id returns id as a string.
func (id SimpleIdentity) Id() string {
	return string(id)
}

// Allow implements ACLIdentity by allowing the identity access to
// ACL members that are equal to id. That is, some user u is considered
// a member of group u and no other.
func (id SimpleIdentity) Allow(ctx context.Context, acl []string) (bool, error) {
	for _, g := range acl {
		if string(id) == g {
			return true, nil
		}
	}
	return false, nil
}
