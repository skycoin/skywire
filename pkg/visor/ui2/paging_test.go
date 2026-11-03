package ui2

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaginate(t *testing.T) {
	rows := make([]int, 250)
	page, p := Paginate(rows, ListQuery{Page: 3}, 100)
	require.Len(t, page, 50)
	require.Equal(t, 3, p.Pages())
	require.Equal(t, 201, p.From())
	require.Equal(t, 250, p.To())

	_, p = Paginate(rows, ListQuery{Page: 9}, 100)
	require.Equal(t, 3, p.Page, "a page past the end moves to the last")
	_, p = Paginate(rows, ListQuery{}, 100)
	require.Equal(t, 1, p.Page)

	page, p = Paginate([]int{}, ListQuery{Page: 2}, 100)
	require.Empty(t, page)
	require.Equal(t, 1, p.Page)
	require.Equal(t, 0, p.From())
}

func TestTransportsSelect(t *testing.T) {
	var tv Transports
	for i := range 150 {
		tv.List = append(tv.List, Transport{ID: fmt.Sprintf("id-%03d", i), Remote: fmt.Sprintf("%03d", 149-i), Type: "stcpr", sentN: uint64(i)})
	}
	tv.List[7].Type = "dmsg"

	tv.Select(ListQuery{Q: "DMSG"})
	require.Len(t, tv.Rows, 1)
	require.Equal(t, "id-007", tv.Rows[0].ID)

	tv.Select(ListQuery{Sort: "sent", Desc: true})
	require.Len(t, tv.Rows, ListPageSize)
	require.Equal(t, "id-149", tv.Rows[0].ID)

	tv.Select(ListQuery{Sort: "remote", Page: 2})
	require.Len(t, tv.Rows, 50)
	require.Equal(t, "100", tv.Rows[0].Remote)
}

func TestParseListQuery(t *testing.T) {
	v := map[string]string{"q": " abc ", "sort": "type", "desc": "1", "page": "x"}
	q := ParseListQuery(func(k string) string { return v[k] })
	require.Equal(t, ListQuery{Q: "abc", Sort: "type", Desc: true}, q)
	require.True(t, q.Match("xxABCxx"))
	require.False(t, q.Match("ab", "c"))
}

func TestSelectRules(t *testing.T) {
	var all []Rule
	for i := range 150 {
		all = append(all, Rule{Key: fmt.Sprint(i), Type: "Consume"})
	}
	all[42].Type = "Forward"
	r := SelectRules("pk", all, ListQuery{Sort: "key", Desc: true, Page: 2}, "")
	require.Equal(t, 150, r.Total)
	require.Len(t, r.Rows, 50)
	require.Equal(t, "49", r.Rows[0].Key)
	r = SelectRules("pk", all, ListQuery{Q: "forward"}, "")
	require.Len(t, r.Rows, 1)
	require.Equal(t, "42", r.Rows[0].Key)
}
