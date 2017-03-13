package agent

/*
PROTOCOL

The agent protocol is initiated when attempting to perform a
discharge. It works as follows:

A is Agent
L is Login Service

A->L
	POST /discharge
L->A
	Interaction-required error containing
	an entry for "agent" with field
	"login-url" holding URL $loginURL.
A->L
	GET $loginURL?username=$user&public-key=$pubkey
	where $user is the username to log in as
	and $pubkey is the public key of that username,
	base-64 encoded.
L->A
	JSON response:
		macaroon:
			macaroon with "local" third-party-caveat
			addressed to $pubkey.
A->L
	POST /discharge?token-kind=agent&token64=self-discharged macaroon
	The macaroon is binary-encoded, then base64
	encoded. Note that, as with most Go HTTP handlers, the parameters
	may also be in the form-encoded request body.
L->A
	discharge macaroon

A local third-party caveat is a third party caveat with the location
set to "local" and the caveat encrypted with the public key specified in the GET request.

LEGACY PROTOCOL

The legacy agent protocol is used by services that don't yet
implement the new protocol. Once a discharge has
failed with an interaction required error, an agent login works
as follows:

        Agent                            Login Service
          |                                    |
          | GET visitURL with agent cookie     |
          |----------------------------------->|
          |                                    |
          |    Macaroon with local third-party |
          |                             caveat |
          |<-----------------------------------|
          |                                    |
          | GET visitURL with agent cookie &   |
          | discharged macaroon                |
          |----------------------------------->|
          |                                    |
          |               Agent login response |
          |<-----------------------------------|
          |                                    |

The agent cookie is a cookie in the same form described in the
PROTOCOL section above.

On success the response is the following JSON object:

{
    "agent_login": "true"
}

If an error occurs then the response should be a JSON object that
unmarshals to an httpbakery.Error.
*/
