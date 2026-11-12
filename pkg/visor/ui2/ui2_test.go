package ui2

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/contacts"
)

func TestFormRoundTrip(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	in := contacts.Contact{
		UID:      "u1",
		Name:     "Alice",
		Keys:     []cipher.PubKey{pk},
		Mail:     []string{"a@example.org", "b@example.org"},
		Payments: []contacts.Payment{{Coin: "skycoin", Address: "2jBbGx"}},
		Groups:   []string{"work", "friends"},
		Notes:    "hi",
	}
	out, err := FormOf("pk", in).Contact()
	require.NoError(t, err)
	require.Equal(t, in, out)

	_, err = Form{Name: "x", Keys: "nope"}.Contact()
	require.Error(t, err)
	_, err = Form{Name: "x", Payments: "noscheme"}.Contact()
	require.Error(t, err)
}

func TestShown(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	l := List{All: []contacts.Contact{
		{Name: "Alice", Groups: []string{"work"}},
		{Name: "Bob", Keys: []cipher.PubKey{pk}},
	}}
	require.Len(t, l.Shown(), 2)
	l.Filter = "WORK"
	require.Equal(t, "Alice", l.Shown()[0].Name)
	l.Filter = pk.Hex()[10:30]
	require.Equal(t, "Bob", l.Shown()[0].Name)
	l.Filter = "zzz"
	require.Empty(t, l.Shown())
}

func TestNodesRender(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	ns := []Node{
		{PK: "b", Hostname: "zeta", Online: true},
		{PK: pk.Hex(), Hostname: "hv", Online: true, IsHV: true, Transports: 3, Apps: 4, AppsRunning: 2, Reward: "x"},
		{PK: "a", Hostname: "alpha", LastSeen: "2026-11-03 10:00:00"},
	}
	SortNodes(ns)
	require.Equal(t, []string{"hv", "zeta", "alpha"}, []string{ns[0].Hostname, ns[1].Hostname, ns[2].Hostname})

	ctx := With(context.Background(), Page{Base: "../", Lang: "en"}, NewCatalog(nil))
	var b strings.Builder
	require.NoError(t, NodesPage(ns, "").Render(ctx, &b))
	out := b.String()
	require.Contains(t, out, `<base href="../">`)
	require.Contains(t, out, `data-href="visors/`+pk.Hex()+`/info"`)
	require.Contains(t, out, pk.Hex(), "keys are shown whole")
	require.Contains(t, out, "2 / 4")
	require.Contains(t, out, `hx-get="nodes"`)
}

func TestBaseFor(t *testing.T) {
	require.Equal(t, "./", BaseFor("/"))
	require.Equal(t, "./", BaseFor("/login"))
	require.Equal(t, "../../", BaseFor("/visors/pk/info"))
	require.Equal(t, "./", BaseFor("/"))
}

func TestPickLang(t *testing.T) {
	require.Equal(t, "de", PickLang("de", "zh"))
	require.Equal(t, "zh", PickLang("xx", "fr-FR, zh-CN;q=0.8"))
	require.Equal(t, "en", PickLang("", "fr"))
}

func TestCatalogFallback(t *testing.T) {
	c := NewCatalog(fstest.MapFS{
		"assets/i18n/en.json": {Data: []byte(`{"ui2": {"classic": "Classic UI", "visors-count": "{{ n }} visors"}}`)},
		"assets/i18n/zh.json": {Data: []byte(`{"ui2": {"theme": "主题"}}`)},
	})
	require.Equal(t, "Classic UI", c.Get("zh", "ui2.classic"), "a key zh lacks falls back to English")
	require.Equal(t, "主题", c.Get("zh", "ui2.theme"))
	require.Equal(t, "no.such.key", c.Get("en", "no.such.key"))
	ctx := With(context.Background(), Page{Lang: "en"}, c)
	require.Equal(t, "3 visors", T(ctx, "ui2.visors-count", "n", "3"))
}
