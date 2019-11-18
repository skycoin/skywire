package router

import (
	"bytes"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
)

func TestNewRouteGroup(t *testing.T) {
	rt := routing.NewTable(routing.DefaultConfig())

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	port1 := routing.Port(1)
	port2 := routing.Port(2)
	desc := routing.NewRouteDescriptor(pk1, pk2, port1, port2)

	rg := NewRouteGroup(rt, desc)
	require.NotNil(t, rg)

	require.NoError(t, rg.Close())
}

func TestRouteGroup_LocalAddr(t *testing.T) {
	type fields struct {
		mu       sync.RWMutex
		logger   *logging.Logger
		desc     routing.RouteDescriptor
		rt       routing.Table
		tps      []*transport.ManagedTransport
		fwd      []routing.Rule
		rvs      []routing.Rule
		lastSent int64
		readCh   chan []byte
		readBuf  bytes.Buffer
		done     chan struct{}
		once     sync.Once
	}
	tests := []struct {
		name   string
		fields fields
		want   net.Addr
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RouteGroup{
				mu:       tt.fields.mu,
				logger:   tt.fields.logger,
				desc:     tt.fields.desc,
				rt:       tt.fields.rt,
				tps:      tt.fields.tps,
				fwd:      tt.fields.fwd,
				rvs:      tt.fields.rvs,
				lastSent: tt.fields.lastSent,
				readCh:   tt.fields.readCh,
				readBuf:  tt.fields.readBuf,
				done:     tt.fields.done,
				once:     tt.fields.once,
			}
			if got := r.LocalAddr(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LocalAddr() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRouteGroup_Read(t *testing.T) {
	type fields struct {
		mu       sync.RWMutex
		logger   *logging.Logger
		desc     routing.RouteDescriptor
		rt       routing.Table
		tps      []*transport.ManagedTransport
		fwd      []routing.Rule
		rvs      []routing.Rule
		lastSent int64
		readCh   chan []byte
		readBuf  bytes.Buffer
		done     chan struct{}
		once     sync.Once
	}
	type args struct {
		p []byte
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantN   int
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RouteGroup{
				mu:       tt.fields.mu,
				logger:   tt.fields.logger,
				desc:     tt.fields.desc,
				rt:       tt.fields.rt,
				tps:      tt.fields.tps,
				fwd:      tt.fields.fwd,
				rvs:      tt.fields.rvs,
				lastSent: tt.fields.lastSent,
				readCh:   tt.fields.readCh,
				readBuf:  tt.fields.readBuf,
				done:     tt.fields.done,
				once:     tt.fields.once,
			}
			gotN, err := r.Read(tt.args.p)
			if (err != nil) != tt.wantErr {
				t.Errorf("Read() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotN != tt.wantN {
				t.Errorf("Read() gotN = %v, want %v", gotN, tt.wantN)
			}
		})
	}
}

func TestRouteGroup_RemoteAddr(t *testing.T) {
	type fields struct {
		mu       sync.RWMutex
		logger   *logging.Logger
		desc     routing.RouteDescriptor
		rt       routing.Table
		tps      []*transport.ManagedTransport
		fwd      []routing.Rule
		rvs      []routing.Rule
		lastSent int64
		readCh   chan []byte
		readBuf  bytes.Buffer
		done     chan struct{}
		once     sync.Once
	}
	tests := []struct {
		name   string
		fields fields
		want   net.Addr
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RouteGroup{
				mu:       tt.fields.mu,
				logger:   tt.fields.logger,
				desc:     tt.fields.desc,
				rt:       tt.fields.rt,
				tps:      tt.fields.tps,
				fwd:      tt.fields.fwd,
				rvs:      tt.fields.rvs,
				lastSent: tt.fields.lastSent,
				readCh:   tt.fields.readCh,
				readBuf:  tt.fields.readBuf,
				done:     tt.fields.done,
				once:     tt.fields.once,
			}
			if got := r.RemoteAddr(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RemoteAddr() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRouteGroup_SetDeadline(t *testing.T) {
	type fields struct {
		mu       sync.RWMutex
		logger   *logging.Logger
		desc     routing.RouteDescriptor
		rt       routing.Table
		tps      []*transport.ManagedTransport
		fwd      []routing.Rule
		rvs      []routing.Rule
		lastSent int64
		readCh   chan []byte
		readBuf  bytes.Buffer
		done     chan struct{}
		once     sync.Once
	}
	type args struct {
		t time.Time
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RouteGroup{
				mu:       tt.fields.mu,
				logger:   tt.fields.logger,
				desc:     tt.fields.desc,
				rt:       tt.fields.rt,
				tps:      tt.fields.tps,
				fwd:      tt.fields.fwd,
				rvs:      tt.fields.rvs,
				lastSent: tt.fields.lastSent,
				readCh:   tt.fields.readCh,
				readBuf:  tt.fields.readBuf,
				done:     tt.fields.done,
				once:     tt.fields.once,
			}
			if err := r.SetDeadline(tt.args.t); (err != nil) != tt.wantErr {
				t.Errorf("SetDeadline() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRouteGroup_SetReadDeadline(t *testing.T) {
	type fields struct {
		mu       sync.RWMutex
		logger   *logging.Logger
		desc     routing.RouteDescriptor
		rt       routing.Table
		tps      []*transport.ManagedTransport
		fwd      []routing.Rule
		rvs      []routing.Rule
		lastSent int64
		readCh   chan []byte
		readBuf  bytes.Buffer
		done     chan struct{}
		once     sync.Once
	}
	type args struct {
		t time.Time
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RouteGroup{
				mu:       tt.fields.mu,
				logger:   tt.fields.logger,
				desc:     tt.fields.desc,
				rt:       tt.fields.rt,
				tps:      tt.fields.tps,
				fwd:      tt.fields.fwd,
				rvs:      tt.fields.rvs,
				lastSent: tt.fields.lastSent,
				readCh:   tt.fields.readCh,
				readBuf:  tt.fields.readBuf,
				done:     tt.fields.done,
				once:     tt.fields.once,
			}
			if err := r.SetReadDeadline(tt.args.t); (err != nil) != tt.wantErr {
				t.Errorf("SetReadDeadline() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRouteGroup_SetWriteDeadline(t *testing.T) {
	type fields struct {
		mu       sync.RWMutex
		logger   *logging.Logger
		desc     routing.RouteDescriptor
		rt       routing.Table
		tps      []*transport.ManagedTransport
		fwd      []routing.Rule
		rvs      []routing.Rule
		lastSent int64
		readCh   chan []byte
		readBuf  bytes.Buffer
		done     chan struct{}
		once     sync.Once
	}
	type args struct {
		t time.Time
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RouteGroup{
				mu:       tt.fields.mu,
				logger:   tt.fields.logger,
				desc:     tt.fields.desc,
				rt:       tt.fields.rt,
				tps:      tt.fields.tps,
				fwd:      tt.fields.fwd,
				rvs:      tt.fields.rvs,
				lastSent: tt.fields.lastSent,
				readCh:   tt.fields.readCh,
				readBuf:  tt.fields.readBuf,
				done:     tt.fields.done,
				once:     tt.fields.once,
			}
			if err := r.SetWriteDeadline(tt.args.t); (err != nil) != tt.wantErr {
				t.Errorf("SetWriteDeadline() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRouteGroup_Write(t *testing.T) {
	type fields struct {
		mu       sync.RWMutex
		logger   *logging.Logger
		desc     routing.RouteDescriptor
		rt       routing.Table
		tps      []*transport.ManagedTransport
		fwd      []routing.Rule
		rvs      []routing.Rule
		lastSent int64
		readCh   chan []byte
		readBuf  bytes.Buffer
		done     chan struct{}
		once     sync.Once
	}
	type args struct {
		p []byte
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantN   int
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RouteGroup{
				mu:       tt.fields.mu,
				logger:   tt.fields.logger,
				desc:     tt.fields.desc,
				rt:       tt.fields.rt,
				tps:      tt.fields.tps,
				fwd:      tt.fields.fwd,
				rvs:      tt.fields.rvs,
				lastSent: tt.fields.lastSent,
				readCh:   tt.fields.readCh,
				readBuf:  tt.fields.readBuf,
				done:     tt.fields.done,
				once:     tt.fields.once,
			}
			gotN, err := r.Write(tt.args.p)
			if (err != nil) != tt.wantErr {
				t.Errorf("Write() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotN != tt.wantN {
				t.Errorf("Write() gotN = %v, want %v", gotN, tt.wantN)
			}
		})
	}
}

func TestRouteGroup_isClosing(t *testing.T) {
	type fields struct {
		mu       sync.RWMutex
		logger   *logging.Logger
		desc     routing.RouteDescriptor
		rt       routing.Table
		tps      []*transport.ManagedTransport
		fwd      []routing.Rule
		rvs      []routing.Rule
		lastSent int64
		readCh   chan []byte
		readBuf  bytes.Buffer
		done     chan struct{}
		once     sync.Once
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RouteGroup{
				mu:       tt.fields.mu,
				logger:   tt.fields.logger,
				desc:     tt.fields.desc,
				rt:       tt.fields.rt,
				tps:      tt.fields.tps,
				fwd:      tt.fields.fwd,
				rvs:      tt.fields.rvs,
				lastSent: tt.fields.lastSent,
				readCh:   tt.fields.readCh,
				readBuf:  tt.fields.readBuf,
				done:     tt.fields.done,
				once:     tt.fields.once,
			}
			if got := r.isClosing(); got != tt.want {
				t.Errorf("isClosing() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRouteGroup_keepAliveLoop(t *testing.T) {
	type fields struct {
		mu       sync.RWMutex
		logger   *logging.Logger
		desc     routing.RouteDescriptor
		rt       routing.Table
		tps      []*transport.ManagedTransport
		fwd      []routing.Rule
		rvs      []routing.Rule
		lastSent int64
		readCh   chan []byte
		readBuf  bytes.Buffer
		done     chan struct{}
		once     sync.Once
	}
	tests := []struct {
		name   string
		fields fields
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = &RouteGroup{
				mu:       tt.fields.mu,
				logger:   tt.fields.logger,
				desc:     tt.fields.desc,
				rt:       tt.fields.rt,
				tps:      tt.fields.tps,
				fwd:      tt.fields.fwd,
				rvs:      tt.fields.rvs,
				lastSent: tt.fields.lastSent,
				readCh:   tt.fields.readCh,
				readBuf:  tt.fields.readBuf,
				done:     tt.fields.done,
				once:     tt.fields.once,
			}
		})
	}
}

func TestRouteGroup_sendKeepAlive(t *testing.T) {
	type fields struct {
		mu       sync.RWMutex
		logger   *logging.Logger
		desc     routing.RouteDescriptor
		rt       routing.Table
		tps      []*transport.ManagedTransport
		fwd      []routing.Rule
		rvs      []routing.Rule
		lastSent int64
		readCh   chan []byte
		readBuf  bytes.Buffer
		done     chan struct{}
		once     sync.Once
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RouteGroup{
				mu:       tt.fields.mu,
				logger:   tt.fields.logger,
				desc:     tt.fields.desc,
				rt:       tt.fields.rt,
				tps:      tt.fields.tps,
				fwd:      tt.fields.fwd,
				rvs:      tt.fields.rvs,
				lastSent: tt.fields.lastSent,
				readCh:   tt.fields.readCh,
				readBuf:  tt.fields.readBuf,
				done:     tt.fields.done,
				once:     tt.fields.once,
			}
			if err := r.sendKeepAlive(); (err != nil) != tt.wantErr {
				t.Errorf("sendKeepAlive() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
