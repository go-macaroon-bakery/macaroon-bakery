package mgorootkeystore

var (
	TimeNow             = &timeNow
	MgoCollectionFindId = &mgoCollectionFindId
)

type RootKey rootKey

func IsValidWithPolicy(k RootKey, p Policy) bool {
	return rootKey(k).isValidWithPolicy(p)
}
