// Package bakerypb is an internal package that allows us to hide the internal
// serialization details of protobuf serialization used in the bakery.
package bakerypb

import (
	"github.com/golang/protobuf/proto"
)

//go:generate  protoc --go_out . defs.proto

// MarshalBinary implements encoding.BinaryMarshaler.
func (id *MacaroonId) MarshalBinary() ([]byte, error) {
	return proto.Marshal(id)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (id *MacaroonId) UnmarshalBinary(data []byte) error {
	return proto.Unmarshal(data, id)
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (c *ThirdPartyCondition) MarshalBinary() ([]byte, error) {
	return proto.Marshal(c)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (c *ThirdPartyCondition) UnmarshalBinary(data []byte) error {
	return proto.Unmarshal(data, c)
}
