// The bakery package layers on top of the macaroon package, providing
// a transport and storage-agnostic way of using macaroons to assert
// client capabilities.
//
package bakery

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/juju/loggo"
	"github.com/rogpeppe/fastuuid"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

var logger = loggo.GetLogger("bakery")

var uuidGen = fastuuid.MustNewGenerator()

// Version represents a version of the bakery protocol.
type Version int

const (
	// In version 0, discharge-required errors use status 407
	Version0 Version = 0
	// In version 1,  discharge-required errors use status 401.
	Version1 Version = 1
	// In version 2, binary macaroons and caveat ids are supported.
	Version2      Version = 2
	LatestVersion         = Version2
)

// MacaroonVersion returns the macaroon version that should
// be used with the given bakery Version.
func MacaroonVersion(v Version) macaroon.Version {
	switch v {
	case Version0, Version1:
		return macaroon.V1
	default:
		return macaroon.V2
	}
}

// Service represents a service which can use macaroons
// to check authorization.
type Service struct {
	location string
	store    Storage
	key      *KeyPair
	locator  ThirdPartyLocator
	checker  FirstPartyCaveatChecker
}

// NewServiceParams holds the parameters for a NewService call.
type NewServiceParams struct {
	// Location will be set as the location of any macaroons
	// minted by the service.
	Location string

	// Store will be used to store macaroon
	// information locally. If it is nil,
	// an in-memory storage will be used.
	Store Storage

	// Key is the public key pair used by the service for
	// third-party caveat encryption.
	// It may be nil, in which case a new key pair
	// will be generated.
	Key *KeyPair

	// Locator provides public keys for third-party services by location when
	// adding a third-party caveat.
	// It may be nil, in which case, no third-party caveats can be created.
	Locator ThirdPartyLocator

	// Checker is used to check first party caveats.
	// It defaults to checkers.New(nil).
	Checker FirstPartyCaveatChecker
}

// NewService returns a new service that can mint new
// macaroons and store their associated root keys.
func NewService(p NewServiceParams) (*Service, error) {
	if p.Store == nil {
		p.Store = NewMemStorage()
	}
	if p.Key == nil {
		var err error
		p.Key, err = GenerateKey()
		if err != nil {
			return nil, err
		}
	}
	if p.Locator == nil {
		p.Locator = emptyLocator{}
	}
	if p.Checker == nil {
		p.Checker = checkers.New(nil)
	}
	svc := &Service{
		location: p.Location,
		locator:  p.Locator,
		store:    p.Store,
		key:      p.Key,
		checker:  p.Checker,
	}
	return svc, nil
}

type emptyLocator struct{}

func (emptyLocator) ThirdPartyInfo(loc string) (ThirdPartyInfo, error) {
	return ThirdPartyInfo{}, ErrNotFound
}

// WithStore returns a copy of service where macaroon creation and
// lookup uses the given store to look up and create macaroon root
// keys.
//
// When NewMacaroon is called on the returned Service,
// it must always be called with an empty id and rootKey.
func (svc *Service) WithStore(store Storage) *Service {
	svc1 := *svc
	svc1.store = store
	return &svc1
}

// Store returns the store used by the service.
// If the service has a RootKeyStorage (there
// was one specified in the parameters or the service
// was created with WithRootKeyStorage), it
// returns nil.
func (svc *Service) Store() Storage {
	return svc.store
}

// Location returns the service's configured macaroon location.
func (svc *Service) Location() string {
	return svc.location
}

// Locator returns the public key locator used by the service.
func (svc *Service) Locator() ThirdPartyLocator {
	return svc.locator
}

func (svc *Service) Namespace() *checkers.Namespace {
	return svc.checker.Namespace()
}

// PublicKey returns the service's public key.
func (svc *Service) PublicKey() *PublicKey {
	return &svc.key.Public
}

// Key returns the service's private/public key par.
func (svc *Service) Key() *KeyPair {
	return svc.key
}

// Check checks that the given macaroons verify
// correctly using the provided checker to check
// first party caveats. The primary macaroon is in ms[0]; the discharges
// fill the rest of the slice.
//
// If there is a verification error, it returns a VerificationError that
// describes the error (other errors might be returned in other
// circumstances).
func (svc *Service) Check(ctxt context.Context, ms macaroon.Slice) error {
	if len(ms) == 0 {
		return &VerificationError{
			Reason: fmt.Errorf("no macaroons in slice"),
		}
	}
	id := ms[0].Id()

	base64Decoded := false
	if id[0] == 'A' {
		// The first byte is not a version number and it's 'A', which is the
		// base64 encoding of the top 6 bits (all zero) of the version number 2,
		// so we assume that it's the base64 encoding of a new-style
		// macaroon id, so we base64 decode it.
		//
		// Note that old-style ids always start with an ASCII character >= 4
		// (> 32 in fact) so this logic won't be triggered for those.
		dec := make([]byte, base64.RawURLEncoding.DecodedLen(len(id)))
		n, err := base64.RawURLEncoding.Decode(dec, id)
		if err == nil {
			// Set the id only on success - if it's a bad encoding, we'll get a not-found error
			// which is fine because "not found" is a correct description of the issue - we
			// can't find the root key for the given id.
			id = dec[0:n]
			base64Decoded = true
		}
	}
	// Trim any extraneous information from the id before retrieving
	// it from storage, including the UUID that's added when
	// creating macaroons to make all macaroons unique even if
	// they're using the same root key.
	switch id[0] {
	case byte(Version2):
		// Skip the UUID at the start of the id.
		id = id[1+16:]
	default:
		if !base64Decoded && isLowerCaseHexChar(id[0]) {
			// It's an old-style id, probably with a hyphenated UUID.
			// so trim that off.
			if i := bytes.LastIndexByte(id, '-'); i >= 0 {
				id = id[0:i]
			}
		}
	}
	rootKey, err := svc.store.Get(id)
	if err != nil {
		if errgo.Cause(err) != ErrNotFound {
			return errgo.Notef(err, "cannot get macaroon")
		}
		// If the macaroon was not found, it is probably
		// because it's been removed after time-expiry,
		// so return a verification error.
		return &VerificationError{
			Reason: errgo.Newf("macaroon not found in storage"),
		}
	}
	err = ms[0].Verify(rootKey, svc.caveatChecker(ctxt), ms[1:])
	if err != nil {
		return &VerificationError{
			Reason: err,
		}
	}
	return nil
}

func (svc *Service) caveatChecker(ctxt context.Context) func(string) error {
	return func(cav string) error {
		return svc.checker.CheckFirstPartyCaveat(ctxt, cav)
	}
}

func isLowerCaseHexChar(c byte) bool {
	switch {
	case '0' <= c && c <= '9':
		return true
	case 'a' <= c && c <= 'f':
		return true
	}
	return false
}

// CheckAny checks that the given slice of slices contains at least
// one macaroon minted by the given service, using checker to check
// any first party caveats. It returns an error with a
// *bakery.VerificationError cause if the macaroon verification failed.
//
// The assert map holds any required attributes of "declared" attributes,
// overriding any inferences made from the macaroons themselves.
// It has a similar effect to adding a checkers.DeclaredCaveat
// for each key and value, but the error message will be more
// useful.
//
// It adds all the standard caveat checkers to the given checker.
//
// It returns any attributes declared in the successfully validated request
// and the set of macaroons that was successfully checked.
func (svc *Service) CheckAny(ctxt context.Context, mss []macaroon.Slice, assert map[string]string) (map[string]string, macaroon.Slice, error) {
	if len(mss) == 0 {
		return nil, nil, &VerificationError{
			Reason: errgo.Newf("no macaroons"),
		}
	}
	// TODO perhaps return a slice of attribute maps, one
	// for each successfully validated macaroon slice?
	var err error
	for _, ms := range mss {
		declared := checkers.InferDeclared(svc.checker.Namespace(), ms)
		for key, val := range assert {
			declared[key] = val
		}
		err = svc.Check(checkers.ContextWithDeclared(ctxt, declared), ms)
		if err == nil {
			return declared, ms, nil
		}
	}
	// Return an arbitrary error from the macaroons provided.
	// TODO return all errors.
	return nil, nil, errgo.Mask(err, isVerificationError)
}

func isVerificationError(err error) bool {
	_, ok := err.(*VerificationError)
	return ok
}

// NewMacaroon mints a new macaroon with the given caveats
// and version.
// The root key for the macaroon will be obtained from
// the service's Storage.
func (svc *Service) NewMacaroon(version Version, caveats []checkers.Caveat) (*macaroon.Macaroon, error) {
	rootKey, id, err := svc.rootKey()
	if err != nil {
		return nil, errgo.Mask(err)
	}
	if version < Version2 {
		// The macaroon can't take a non-UTF-8 id, so encode
		// it as base64 before using it.
		id1 := make([]byte, base64.RawURLEncoding.EncodedLen(len(id)))
		base64.RawURLEncoding.Encode(id1, id)
		id = id1
	}
	m, err := macaroon.New(rootKey, id, svc.location, MacaroonVersion(version))
	if err != nil {
		return nil, errgo.Notef(err, "cannot bake macaroon")
	}
	for _, cav := range caveats {
		if err := svc.AddCaveat(m, cav); err != nil {
			return nil, errgo.Notef(err, "cannot add caveat")
		}
	}
	return m, nil
}

func (svc *Service) rootKey() ([]byte, []byte, error) {
	rootKey, id, err := svc.store.RootKey()
	if err != nil {
		return nil, nil, errgo.Mask(err)
	}
	// Add a UUID to the end of the id so that even
	// though we may be re-using the same underlying
	// id and root key, all minted macaroons will have
	// unique ids.
	//
	// The format of a macaroon id is:
	//
	//	version[1 byte] = LatestVersion
	//	uuid[16 bytes]
	//	actual id [n bytes]
	uuid := uuidGen.Next()
	finalId := make([]byte, 1+16+len(id))
	finalId[0] = byte(LatestVersion)
	copy(finalId[1:], uuid[:16])
	copy(finalId[1+16:], id)
	return rootKey, finalId, nil
}

// LocalThirdPartyCaveat returns a third-party caveat that, when added
// to a macaroon with AddCaveat, results in a caveat
// with the location "local", encrypted with the given public key.
// This can be automatically discharged by DischargeAllWithKey.
func LocalThirdPartyCaveat(key *PublicKey, version Version) checkers.Caveat {
	var loc string
	if version < Version2 {
		loc = "local " + key.String()
	} else {
		loc = fmt.Sprintf("local %d %s", version, key)
	}
	return checkers.Caveat{
		Location: loc,
	}
}

// AddCaveat adds a caveat to the given macaroon.
//
// It uses the service's key pair and locator to call
// the AddCaveat function.
func (svc *Service) AddCaveat(m *macaroon.Macaroon, cav checkers.Caveat) error {
	return AddCaveat(svc.key, svc.locator, m, cav, svc.checker.Namespace())
}

// AddCaveat adds a caveat to the given macaroon.
//
// If it's a third-party caveat, it encrypts it using
// the given key pair and by looking
// up the location using the given locator.
//
// As a special case, if the caveat's Location field has the prefix
// "local " the caveat is added as a client self-discharge caveat
// using the public key base64-encoded in the rest of the location.
// In this case, the Condition field must be empty. The
// resulting third-party caveat will encode the condition "true"
// encrypted with that public key. See LocalThirdPartyCaveat
// for a way of creating such caveats.
func AddCaveat(key *KeyPair, loc ThirdPartyLocator, m *macaroon.Macaroon, cav checkers.Caveat, ns *checkers.Namespace) error {
	if cav.Location == "" {
		if err := m.AddFirstPartyCaveat(ns.ResolveCaveat(cav).Condition); err != nil {
			return errgo.Mask(err)
		}
		return nil
	}
	var info ThirdPartyInfo
	if localInfo, ok := parseLocalLocation(cav.Location); ok {
		info = localInfo
		cav.Location = "local"
		if cav.Condition != "" {
			return errgo.New("cannot specify caveat condition in local third-party caveat")
		}
		cav.Condition = "true"
	} else {
		var err error
		info, err = loc.ThirdPartyInfo(cav.Location)
		if err != nil {
			return errgo.Notef(err, "cannot find public key for location %q", cav.Location)
		}
	}
	rootKey, err := randomBytes(24)
	if err != nil {
		return errgo.Notef(err, "cannot generate third party secret")
	}
	if m.Version() < macaroon.V2 && info.Version >= Version2 {
		// We can't use later version of caveat ids in earlier macaroons.
		info.Version = Version1
	}
	id, err := encodeCaveatId(cav.Condition, rootKey, info, key)
	if err != nil {
		return errgo.Notef(err, "cannot create third party caveat id at %q", cav.Location)
	}
	if err := m.AddThirdPartyCaveat(rootKey, id, cav.Location); err != nil {
		return errgo.Notef(err, "cannot add third party caveat")
	}
	return nil
}

// parseLocalLocation parses a local caveat location as generated by
// LocalThirdPartyCaveat. This is of the form:
//
//	local <version> <pubkey>
//
// where <version> is the bakery version of the client that we're
// adding the local caveat for.
//
// It returns false if the location does not represent a local
// caveat location.
func parseLocalLocation(loc string) (ThirdPartyInfo, bool) {
	if !strings.HasPrefix(loc, "local ") {
		return ThirdPartyInfo{}, false
	}
	version := Version1
	fields := strings.Fields(loc)
	fields = fields[1:] // Skip "local"
	switch len(fields) {
	case 2:
		v, err := strconv.Atoi(fields[0])
		if err != nil {
			return ThirdPartyInfo{}, false
		}
		version = Version(v)
		fields = fields[1:]
		fallthrough
	case 1:
		var key PublicKey
		if err := key.UnmarshalText([]byte(fields[0])); err != nil {
			return ThirdPartyInfo{}, false
		}
		return ThirdPartyInfo{
			PublicKey: key,
			Version:   version,
		}, true
	default:
		return ThirdPartyInfo{}, false
	}
}

// Discharge creates a macaroon that discharges the third party caveat with the
// given id that should have been created earlier using key.Public. The
// condition implicit in the id is checked for validity using checker. If
// it is valid, a new macaroon is returned which discharges the caveat
// along with any caveats returned from the checker.
//
// The macaroon is created with a version derived from the version
// that was used to encode the id.
func Discharge(key *KeyPair, checker ThirdPartyChecker, id []byte) (*macaroon.Macaroon, []checkers.Caveat, error) {
	cavInfo, err := decodeCaveatId(key, []byte(id))
	if err != nil {
		return nil, nil, errgo.Notef(err, "discharger cannot decode caveat id")
	}
	// Note that we don't check the error - we allow the
	// third party checker to see even caveats that we can't
	// understand.
	cond, arg, _ := checkers.ParseCaveat(cavInfo.Condition)

	var caveats []checkers.Caveat
	if cond == checkers.CondNeedDeclared {
		cavInfo.Condition = arg
		caveats, err = checkNeedDeclared(cavInfo, checker)
	} else {
		caveats, err = checker.CheckThirdPartyCaveat(cavInfo)
	}
	if err != nil {
		return nil, nil, errgo.Mask(err, errgo.Any)
	}
	// Note that the discharge macaroon does not need to
	// be stored persistently. Indeed, it would be a problem if
	// we did, because then the macaroon could potentially be used
	// for normal authorization with the third party.
	m, err := macaroon.New(cavInfo.RootKey, id, "", MacaroonVersion(cavInfo.Version))
	if err != nil {
		return nil, nil, errgo.Mask(err)
	}
	return m, caveats, nil
}

// Discharge calls Discharge with the service's key and uses the service
// to add any returned caveats to the discharge macaroon.
// The discharge macaroon will be created with a version
// implied by the id.
func (svc *Service) Discharge(checker ThirdPartyChecker, ns *checkers.Namespace, id []byte) (*macaroon.Macaroon, error) {
	m, caveats, err := Discharge(svc.key, checker, id)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	for _, cav := range caveats {
		// Note that we don't use svc.AddCaveat to add the caveats
		// because we want to use the namespace of the first party
		// that we're discharging for, not the namespace of the
		// discharging service.
		if err := AddCaveat(svc.key, svc.locator, m, cav, ns); err != nil {
			return nil, errgo.Notef(err, "cannot add caveat")
		}
	}
	return m, nil
}

func checkNeedDeclared(cavInfo *ThirdPartyCaveatInfo, checker ThirdPartyChecker) ([]checkers.Caveat, error) {
	arg := cavInfo.Condition
	i := strings.Index(arg, " ")
	if i <= 0 {
		return nil, errgo.Newf("need-declared caveat requires an argument, got %q", arg)
	}
	needDeclared := strings.Split(arg[0:i], ",")
	for _, d := range needDeclared {
		if d == "" {
			return nil, errgo.New("need-declared caveat with empty required attribute")
		}
	}
	if len(needDeclared) == 0 {
		return nil, fmt.Errorf("need-declared caveat with no required attributes")
	}
	cavInfo.Condition = arg[i+1:]
	caveats, err := checker.CheckThirdPartyCaveat(cavInfo)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	declared := make(map[string]bool)
	for _, cav := range caveats {
		if cav.Location != "" {
			continue
		}
		// Note that we ignore the error. We allow the service to
		// generate caveats that we don't understand here.
		cond, arg, _ := checkers.ParseCaveat(cav.Condition)
		if cond != checkers.CondDeclared {
			continue
		}
		parts := strings.SplitN(arg, " ", 2)
		if len(parts) != 2 {
			return nil, errgo.Newf("declared caveat has no value")
		}
		declared[parts[0]] = true
	}
	// Add empty declarations for everything mentioned in need-declared
	// that was not actually declared.
	for _, d := range needDeclared {
		if !declared[d] {
			caveats = append(caveats, checkers.DeclaredCaveat(d, ""))
		}
	}
	return caveats, nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, fmt.Errorf("cannot generate %d random bytes: %v", n, err)
	}
	return b, nil
}

type VerificationError struct {
	Reason error
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("verification failed: %v", e.Reason)
}

// ThirdPartyCaveatInfo holds the information decoded from
// a third party caveat id.
type ThirdPartyCaveatInfo struct {
	// Condition holds the third party condition to be discharged.
	// This is the only field that most third party dischargers will
	// need to consider.
	Condition string

	// FirstPartyPublicKey holds the public key of the party
	// that created the third party caveat.
	FirstPartyPublicKey PublicKey

	// ThirdPartyKeyPair holds the key pair used to decrypt
	// the caveat - the key pair of the discharging service.
	ThirdPartyKeyPair KeyPair

	// RootKey holds the secret root key encoded by the caveat.
	RootKey []byte

	// CaveatId holds the full encoded caveat id from which all
	// the other fields are derived.
	CaveatId []byte

	// MacaroonId holds the id that the discharge macaroon
	// should be given. This is often the same as the
	// CaveatId field.
	MacaroonId []byte

	// Version holds the version that was used to encode
	// the caveat id.
	Version Version
}

// ThirdPartyChecker holds a function that checks third party caveats
// for validity. If the caveat is valid, it returns a nil error and
// optionally a slice of extra caveats that will be added to the
// discharge macaroon. The caveatId parameter holds the still-encoded id
// of the caveat.
//
// If the caveat kind was not recognised, the checker should return an
// error with a ErrCaveatNotRecognized cause.
type ThirdPartyChecker interface {
	CheckThirdPartyCaveat(info *ThirdPartyCaveatInfo) ([]checkers.Caveat, error)
}

type ThirdPartyCheckerFunc func(*ThirdPartyCaveatInfo) ([]checkers.Caveat, error)

func (c ThirdPartyCheckerFunc) CheckThirdPartyCaveat(info *ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	return c(info)
}

// FirstPartyCaveatChecker is used to check first party caveats
// for validity with respect to information in the provided context.
//
// If the caveat kind was not recognised, the checker should return
// checkers.ErrCaveatNotRecognized.
type FirstPartyCaveatChecker interface {
	// CheckFirstPartyCaveat checks that the given caveat condition
	// is valid with respect to the given context information.
	CheckFirstPartyCaveat(ctxt context.Context, caveat string) error

	// Namespace returns the namespace associated with the
	// caveat checker.
	Namespace() *checkers.Namespace
}
