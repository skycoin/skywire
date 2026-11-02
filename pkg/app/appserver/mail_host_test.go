package appserver

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

type fakeMailHost struct{ pk cipher.PubKey }

func (h fakeMailHost) MailSeal(msg []byte, _ cipher.PubKey) ([]byte, error) {
	return append([]byte("sealed:"), msg...), nil
}

func (h fakeMailHost) MailOpen(body []byte) ([]byte, cipher.PubKey, error) {
	if len(body) == 0 {
		return nil, cipher.PubKey{}, errors.New("empty")
	}
	return body, h.pk, nil
}

func (h fakeMailHost) MailHostInfo() MailHostInfo {
	return MailHostInfo{Hypervisors: []cipher.PubKey{h.pk}, PortServed: true}
}

func (h fakeMailHost) MailPark(rcpt cipher.PubKey, _ []byte, _ string) (cipher.PubKey, error) {
	return rcpt, nil
}

func (h fakeMailHost) MailUnpark() ([]ParkedMail, error) {
	return []ParkedMail{{ID: "1", Host: h.pk, From: h.pk, Body: []byte("env")}}, nil
}

func (h fakeMailHost) MailAck([]ParkedRef) error { return nil }

// TestMailHostGateway checks the mail calls reach the visor's MailHost only
// for the app it serves them to.
func TestMailHostGateway(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	m := &procManager{}
	mail := NewRPCGateway(nil, &Proc{m: m, appName: "skymail"})
	chat := NewRPCGateway(nil, &Proc{m: m, appName: "skychat"})

	var info MailHostInfo
	require.ErrorIs(t, mail.MailHostInfo(&struct{}{}, &info), ErrNoMailHost, "no host wired")

	m.SetMailHostFn(func(app string) MailHost {
		if app != "skymail" {
			return nil
		}
		return fakeMailHost{pk}
	})
	require.NoError(t, mail.MailHostInfo(&struct{}{}, &info))
	require.True(t, info.PortServed)
	require.Equal(t, []cipher.PubKey{pk}, info.Hypervisors)

	var sealed []byte
	require.NoError(t, mail.MailSeal(&MailSealReq{Msg: []byte("m"), Rcpt: pk}, &sealed))
	require.Equal(t, "sealed:m", string(sealed))
	body := []byte("x")
	var opened MailOpenResp
	require.NoError(t, mail.MailOpen(&body, &opened))
	require.Equal(t, pk, opened.Signer)
	empty := []byte{}
	require.Error(t, mail.MailOpen(&empty, &opened), "the host's error comes back")

	var host cipher.PubKey
	require.NoError(t, mail.MailPark(&MailParkReq{Rcpt: pk, Env: []byte("env"), Ref: "r"}, &host))
	require.Equal(t, pk, host)
	var parked []ParkedMail
	require.NoError(t, mail.MailUnpark(&struct{}{}, &parked))
	require.Len(t, parked, 1)
	refs := []ParkedRef{{Host: pk, ID: "1"}}
	require.NoError(t, mail.MailAck(&refs, &struct{}{}))
	require.ErrorIs(t, chat.MailUnpark(&struct{}{}, &parked), ErrNoMailHost, "another app may not take the visor's parked mail")
	require.ErrorIs(t, chat.MailPark(&MailParkReq{Rcpt: pk}, &host), ErrNoMailHost)

	require.ErrorIs(t, chat.MailOpen(&body, &opened), ErrNoMailHost, "another app may not open the visor's mail")
	require.ErrorIs(t, chat.MailSeal(&MailSealReq{Msg: []byte("m")}, &sealed), ErrNoMailHost)
	require.ErrorIs(t, NewRPCGateway(nil, nil).MailHostInfo(&struct{}{}, &info), ErrNoMailHost)
}
