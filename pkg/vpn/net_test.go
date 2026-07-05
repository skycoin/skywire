package vpn

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type payload struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

func TestWriteReadJSON_RoundTrip(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close() //nolint:errcheck
	defer srv.Close() //nolint:errcheck

	want := payload{Name: "vpn", N: 7}
	go func() {
		_ = WriteJSON(srv, want) //nolint
	}()

	var got payload
	require.NoError(t, ReadJSON(cli, &got))
	require.Equal(t, want, got)
}

func TestWriteReadJSONWithTimeout_RoundTrip(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close() //nolint:errcheck
	defer srv.Close() //nolint:errcheck

	want := payload{Name: "timeout", N: 42}
	go func() {
		_ = WriteJSONWithTimeout(srv, want, 2*time.Second) //nolint
	}()

	var got payload
	require.NoError(t, ReadJSONWithTimeout(cli, &got, 2*time.Second))
	require.Equal(t, want, got)
}

func TestWriteJSON_MarshalError(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close() //nolint:errcheck
	defer srv.Close() //nolint:errcheck

	// A channel cannot be marshaled to JSON.
	err := WriteJSON(srv, make(chan int))
	require.Error(t, err)
	require.Contains(t, err.Error(), "marshaling")
}

func TestReadJSON_UnmarshalError(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close() //nolint:errcheck
	defer srv.Close() //nolint:errcheck

	go func() {
		_, _ = srv.Write([]byte("{not valid json")) //nolint
		_ = srv.Close()                             //nolint
	}()

	var got payload
	err := ReadJSON(cli, &got)
	require.Error(t, err)
}

func TestReadJSON_ReadError(t *testing.T) {
	cli, srv := net.Pipe()
	require.NoError(t, srv.Close()) // closed → reader's Read fails immediately

	var got payload
	err := ReadJSON(cli, &got)
	require.Error(t, err)
	_ = cli.Close() //nolint
}

func TestWriteJSONWithTimeout_WriteError(t *testing.T) {
	cli, srv := net.Pipe()
	require.NoError(t, cli.Close())
	require.NoError(t, srv.Close()) // peer closed → write fails

	err := WriteJSONWithTimeout(srv, payload{Name: "x"}, time.Second)
	require.Error(t, err)
}
