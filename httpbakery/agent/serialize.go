package agent

import (
	"encoding/json"

	"gopkg.in/errgo.v1"
	"gopkg.in/yaml.v2"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
)

// Agents holds the serialized form of a Visitor - it is
// used by the JSON and YAML marshal and unmarshal
// methods to serialize and deserialize a Visitor.
// Note that any agents with a key pair that matches
// Key will be serialized with empty keys.
type Agents struct {
	Key    *bakery.KeyPair `json:"key,omitempty" yaml:"key,omitempty"`
	Agents []Agent         `json:"agents" yaml:"agents"`
}

var _ interface {
	json.Unmarshaler
	json.Marshaler
	yaml.Unmarshaler
	yaml.Marshaler
} = (*Visitor)(nil)

// UnmarshalJSON implements json.Unmarshaler.
func (v *Visitor) UnmarshalJSON(data []byte) error {
	var adata Agents
	if err := json.Unmarshal(data, &adata); err != nil {
		return errgo.Mask(err)
	}
	return v.setAgentsData(&adata)
}

// MarshalJSON implements JSON.Marshaler.
func (v *Visitor) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.agentsData())
}

// MarshalYAML implements yaml.Marshaler
func (v *Visitor) MarshalYAML() (interface{}, error) {
	return v.agentsData(), nil
}

// UmmarshalYAML implements yaml.Unmarshaler.
func (v *Visitor) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var adata Agents
	if err := unmarshal(&adata); err != nil {
		return errgo.Mask(err)
	}
	return v.setAgentsData(&adata)
}

func (v *Visitor) agentsData() *Agents {
	adata := Agents{
		Key:    v.defaultKey,
		Agents: v.Agents(),
	}
	if adata.Key == nil {
		return &adata
	}
	// Omit all keys that match the default key.
	for _, a := range adata.Agents {
		if *a.Key == *adata.Key {
			a.Key = nil
		}
	}
	return &adata
}

func (v *Visitor) setAgentsData(adata *Agents) error {
	v.defaultKey = adata.Key
	v.agents = make(map[string][]agent)
	// Add the agents in reverse order so that the precedence remains the same.
	for i := len(adata.Agents) - 1; i >= 0; i-- {
		a := adata.Agents[i]
		if err := v.AddAgent(a); err != nil {
			return errgo.Notef(err, "cannot add agent at URL %q", a.URL)
		}
	}
	return nil
}
