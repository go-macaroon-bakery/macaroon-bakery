package agent_test

import (
	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery/agent"
)

func ExampleSetUpAuth() {
	// In practice the key would be read from persistent
	// storage.
	key, err := bakery.GenerateKey()
	if err != nil {
		// handle error
	}

	client := httpbakery.NewClient()
	err = agent.SetUpAuth(client, &agent.AuthInfo{
		Key: key,
		Agents: []agent.Agent{{
			URL:      "http://foo.com",
			Username: "agent-username",
		}},
	})
	if err != nil {
		// handle error
	}
}
