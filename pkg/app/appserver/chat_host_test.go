package appserver

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

type fakeChatHost struct{ pk cipher.PubKey }

func (h fakeChatHost) ChatPark(rcpt cipher.PubKey, _ []byte, _ string) (cipher.PubKey, error) {
	return h.pk, nil
}

func (h fakeChatHost) ChatUnpark() ([]ParkedChat, error) {
	return []ParkedChat{{ID: "1", Host: h.pk, From: h.pk, Msg: []byte("m")}}, nil
}

func (h fakeChatHost) ChatAck([]ParkedRef) error { return nil }

// TestChatHostGateway checks the chat calls reach the visor's ChatHost only
// for the app it serves them to.
func TestChatHostGateway(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	m := &procManager{}
	chat := NewRPCGateway(nil, &Proc{m: m, appName: "skychat"})
	mail := NewRPCGateway(nil, &Proc{m: m, appName: "skymail"})

	var host cipher.PubKey
	require.ErrorIs(t, chat.ChatPark(&ChatParkReq{Rcpt: pk}, &host), ErrNoChatHost, "no host wired")

	m.SetChatHostFn(func(app string) ChatHost {
		if app != "skychat" {
			return nil
		}
		return fakeChatHost{pk}
	})
	require.NoError(t, chat.ChatPark(&ChatParkReq{Rcpt: pk, Msg: []byte("m"), Ref: "r"}, &host))
	require.Equal(t, pk, host)
	var parked []ParkedChat
	require.NoError(t, chat.ChatUnpark(&struct{}{}, &parked))
	require.Len(t, parked, 1)
	refs := []ParkedRef{{Host: pk, ID: "1"}}
	require.NoError(t, chat.ChatAck(&refs, &struct{}{}))

	require.ErrorIs(t, mail.ChatUnpark(&struct{}{}, &parked), ErrNoChatHost, "another app may not take the visor's chat messages")
	require.ErrorIs(t, mail.ChatPark(&ChatParkReq{Rcpt: pk}, &host), ErrNoChatHost)
	require.ErrorIs(t, NewRPCGateway(nil, nil).ChatAck(&refs, &struct{}{}), ErrNoChatHost)
}
