package commands

import (
	"context"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/dm"
)

// appChatParker parks and fetches 1:1 messages through the visor's dmsg-async
// inbox. The visor seals and opens them with its key.
type appChatParker struct{ cl *app.Client }

func (p appChatParker) Park(_ context.Context, pk cipher.PubKey, frame []byte, ref string) (cipher.PubKey, error) {
	return p.cl.ChatPark(pk, frame, ref)
}

func (p appChatParker) Unpark(context.Context) ([]dm.Parked, error) {
	list, err := p.cl.ChatUnpark()
	if err != nil {
		return nil, err
	}
	out := make([]dm.Parked, len(list))
	for i, m := range list {
		out[i] = dm.Parked{ID: m.ID, Host: m.Host, From: m.From, Frame: m.Msg, At: m.At}
	}
	return out, nil
}

func (p appChatParker) Ack(_ context.Context, done []dm.Parked) error {
	refs := make([]appserver.ParkedRef, len(done))
	for i, d := range done {
		refs[i] = appserver.ParkedRef{Host: d.Host, ID: d.ID}
	}
	return p.cl.ChatAck(refs)
}
