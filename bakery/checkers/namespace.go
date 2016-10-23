package checkers

// Namespace holds maps from schema URIs to the
// prefixes that are used to encode them in first party
// caveats. Several different URIs may map to the same
// prefix - this is usual when several different backwardly
// compatible schema versions are registered.
type Namespace struct {
	uriToPrefix map[string]string
}

// NewNamespace returns a new namespace with the
// given initial contents.
func NewNamespace(uriToPrefix map[string]string) *Namespace {
	ns := &Namespace{
		uriToPrefix: make(map[string]string),
	}
	for uri, prefix := range uriToPrefix {
		ns.uriToPrefix[uri] = prefix
	}
	return ns
}

// EnsureResolved tries to resolve the given schema URI to a prefix and
// returns the prefix and whether the resolution was successful. If the
// URI hasn't been registered but a compatible version has, the
// given URI is registered with the same prefix.
func (ns *Namespace) EnsureResolved(uri string) (string, bool) {
	// TODO(rog) compatibility
	return ns.Resolve(uri)
}

// Resolve resolves the given schema URI to its registered prefix and
// returns the prefix and whether the resolution was successful.
//
// If ns is nil, it is treated as if it were empty.
//
// Resolve does not mutate ns and may be called concurrently
// with other non-mutating Namespace methods.
func (ns *Namespace) Resolve(uri string) (string, bool) {
	if ns == nil {
		return "", false
	}
	prefix, ok := ns.uriToPrefix[uri]
	return prefix, ok
}

// ResolveCaveat resolves the given caveat by using
// Resolve to map from its schema namespace to the appropriate prefix using
// Resolve. If there is no registered prefix for the namespace,
// it returns an error caveat.
//
// If ns.Namespace is empty or ns.Location is non-empty, it returns cav unchanged.
//
// If ns is nil, it is treated as if it were empty.
//
// ResolveCaveat does not mutate ns and may be called concurrently
// with other non-mutating Namespace methods.
func (ns *Namespace) ResolveCaveat(cav Caveat) Caveat {
	// TODO(rog) If a namespace isn't registered, try to resolve it by
	// resolving it to the latest compatible version that is
	// registered.
	if cav.Namespace == "" || cav.Location != "" {
		return cav
	}
	prefix, ok := ns.Resolve(cav.Namespace)
	if !ok {
		return ErrorCaveatf("caveat %q in unregistered namespace %q", cav.Condition, cav.Namespace)
	}
	if prefix != "" {
		cav.Condition = ConditionWithPrefix(prefix, cav.Condition)
	}
	cav.Namespace = ""
	return cav
}

// ConditionWithPrefix returns the given string prefixed by the
// given prefix. If the prefix is non-empty, a colon
// is used to separate them.
func ConditionWithPrefix(prefix, s string) string {
	if prefix == "" {
		return s
	}
	return prefix + ":" + s
}

// Register registers the given URI and associates it
// with the given prefix. If the URI has already been registered,
// this is a no-op.
func (ns *Namespace) Register(uri, prefix string) {
	if _, ok := ns.uriToPrefix[uri]; !ok {
		ns.uriToPrefix[uri] = prefix
	}
}
