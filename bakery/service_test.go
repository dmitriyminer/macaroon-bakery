package bakery_test

import (
	"fmt"

	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v0/bakery"
	"gopkg.in/macaroon.v1"
)

type ServiceSuite struct{}

var _ = gc.Suite(&ServiceSuite{})

// TestSingleServiceFirstParty creates a single service
// with a macaroon with one first party caveat.
// It creates a request with this macaroon and checks that the service
// can verify this macaroon as valid.
func (s *ServiceSuite) TestSingleServiceFirstParty(c *gc.C) {
	p := bakery.NewServiceParams{
		Location: "loc",
		Store:    nil,
		Key:      nil,
		Locator:  nil,
	}
	service, err := bakery.NewService(p)
	c.Assert(err, gc.IsNil)

	macaroon, err := service.NewMacaroon("", nil, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(macaroon.Location(), gc.Equals, "loc")
	cav := bakery.Caveat{
		Location:  "",
		Condition: "something",
	}
	err = service.AddCaveat(macaroon, cav)
	c.Assert(err, gc.IsNil)

	checker := strCompFirstPartyChecker("something")
	req := service.NewRequest(checker)

	req.AddClientMacaroon(macaroon)

	err = req.Check()
	c.Assert(err, gc.IsNil)
}

// TestMacaroonPaperFig6 implements an example flow as described in the macaroons paper:
// http://theory.stanford.edu/~ataly/Papers/macaroons.pdf
// There are three services, ts, fs, as:
// ts is a storage service which has deligated authority to a forum service fs.
// The forum service wants to require its users to be logged into to an authentication service as.
//
// The client obtains a macaroon from fs (minted by ts, with a third party caveat addressed to as).
// The client obtains a discharge macaroon from as to satisfy this caveat.
// The target service verifies the original macaroon it delegated to fs
// No direct contact between as and ts is required
func (s *ServiceSuite) TestMacaroonPaperFig6(c *gc.C) {
	ts, fs, as := setUpFig6Services(c)
	tsMacaroon := createMacaroonWithThirdPartyCaveat(c, ts, fs, bakery.Caveat{Location: "as-loc", Condition: "user==bob"})
	d, err := bakery.DischargeAll(tsMacaroon, func(firstPartyLocation string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		c.Assert(firstPartyLocation, gc.Equals, "ts-loc")
		c.Assert(cav.Location, gc.Equals, "as-loc")
		mac, err := as.Discharge(strCompThirdPartyChecker("user==bob"), cav.Id)
		c.Assert(err, gc.IsNil)
		return mac, nil
	})
	c.Assert(err, gc.IsNil)

	// client makes request to ts
	req := ts.NewRequest(strCompFirstPartyChecker(""))
	req.AddClientMacaroon(tsMacaroon)
	// client has all the discharge macaroons. For each discharge macaroon bind it to our tsMacaroon
	// and add it to our request.
	for _, dm := range d {
		dm.Bind(tsMacaroon.Signature())
		req.AddClientMacaroon(dm)
	}

	err = req.Check()
	c.Assert(err, gc.IsNil)
}

func (s *ServiceSuite) TestMacaroonPaperFig6FailsWithoutDischarges(c *gc.C) {
	ts, fs, _ := setUpFig6Services(c)
	tsMacaroon := createMacaroonWithThirdPartyCaveat(c, ts, fs, bakery.Caveat{Location: "as-loc", Condition: "user==bob"})

	req := ts.NewRequest(strCompFirstPartyChecker(""))
	req.AddClientMacaroon(tsMacaroon)

	err := req.Check()
	c.Assert(err, gc.ErrorMatches, `verification failed: cannot find discharge macaroon for caveat ".*"`)
}

func (s *ServiceSuite) TestMacaroonPaperFig6FailsWithBindingOnTamperedSignature(c *gc.C) {
	ts, fs, as := setUpFig6Services(c)
	tsMacaroon := createMacaroonWithThirdPartyCaveat(c, ts, fs, bakery.Caveat{Location: "as-loc", Condition: "user==bob"})
	d, err := bakery.DischargeAll(tsMacaroon, func(firstPartyLocation string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		c.Assert(firstPartyLocation, gc.Equals, "ts-loc")
		c.Assert(cav.Location, gc.Equals, "as-loc")
		mac, err := as.Discharge(strCompThirdPartyChecker("user==bob"), cav.Id)
		c.Assert(err, gc.IsNil)
		return mac, nil
	})
	c.Assert(err, gc.IsNil)

	req := ts.NewRequest(strCompFirstPartyChecker(""))
	req.AddClientMacaroon(tsMacaroon)
	for _, dm := range d {
		dm.Bind([]byte("tampered-signature"))
		req.AddClientMacaroon(dm)
	}

	err = req.Check()
	c.Assert(err, gc.ErrorMatches, "verification failed: signature mismatch after caveat verification")
}

func setUpFig6Services(c *gc.C) (*bakery.Service, *bakery.Service, *bakery.Service) {
	fsKeyPair, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	asKeyPair, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	publicKeyLocator := bakery.PublicKeyLocatorMap{
		"fs-loc": &fsKeyPair.Public,
		"as-loc": &asKeyPair.Public,
	}

	ts, err := bakery.NewService(bakery.NewServiceParams{Location: "ts-loc"})
	c.Assert(err, gc.IsNil)
	fs, err := bakery.NewService(bakery.NewServiceParams{
		Location: "fs-loc",
		Key:      fsKeyPair,
		Locator:  publicKeyLocator,
	})
	c.Assert(err, gc.IsNil)
	as, err := bakery.NewService(bakery.NewServiceParams{
		Location: "as-loc",
		Key:      asKeyPair,
		Locator:  publicKeyLocator,
	})
	c.Assert(err, gc.IsNil)

	return ts, fs, as
}

func createMacaroonWithThirdPartyCaveat(c *gc.C, minter, caveater *bakery.Service, cav bakery.Caveat) *macaroon.Macaroon {
	mac, err := minter.NewMacaroon("", nil, nil)
	c.Assert(err, gc.IsNil)

	err = caveater.AddCaveat(mac, cav)
	c.Assert(err, gc.IsNil)
	return mac
}

type strCompFirstPartyChecker string

func (c strCompFirstPartyChecker) CheckFirstPartyCaveat(caveat string) error {
	if caveat != string(c) {
		return fmt.Errorf("%v doesn't match %s", caveat, c)
	}
	return nil
}

type strCompThirdPartyChecker string

func (c strCompThirdPartyChecker) CheckThirdPartyCaveat(caveatId string, caveat string) ([]bakery.Caveat, error) {
	if caveat != string(c) {
		return nil, fmt.Errorf("%v doesn't match %s", caveat, c)
	}
	return nil, nil
}
