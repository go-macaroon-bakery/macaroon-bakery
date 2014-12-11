package checkers

import (
	"fmt"
	"time"

	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v0/bakery"
)

var timeNow = time.Now

// TimeBefore is a checker that checks caveats
// as created by TimeBeforeCaveat.
var TimeBefore = CheckerFunc{
	Condition_: CondTimeBefore,
	Check_: func(_, cav string) error {
		t, err := time.Parse(time.RFC3339Nano, cav)
		if err != nil {
			return errgo.Mask(err)
		}
		if !timeNow().Before(t) {
			return fmt.Errorf("macaroon has expired")
		}
		return nil
	},
}

// TimeBeforeCaveat returns a caveat that specifies that
// the time that it is checked should be before t.
func TimeBeforeCaveat(t time.Time) bakery.Caveat {
	return firstParty(CondTimeBefore, t.UTC().Format(time.RFC3339Nano))
}
