// Package msg represents node messages
package msg

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

// protocol
//
// [1 byte] - type
// [ .... ] - encoded message
//

// Version is current protocol version
const Version uint16 = 3

// be sure that all messages implements Msg interface compiler time
var (

	// pings

	_ Msg = &Ping{} // -> Ping
	_ Msg = &Pong{} // <- Pong

	// handshake

	_ Msg = &Syn{} // <- Syn (node id, protocol version)
	_ Msg = &Ack{} // -> Ack (peer id)

	// common replies

	_ Msg = &Ok{}  // -> Ok ()
	_ Msg = &Err{} // -> Err (error message)

	// subscriptions

	_ Msg = &Sub{}   // <- Sub (feed)
	_ Msg = &Unsub{} // <- Unsub (feed)

	// public server features

	_ Msg = &RqList{} // <- GetList ()
	_ Msg = &List{}   // -> Lsit  (feeds)

	// root (push and done)

	_ Msg = &Root{} // <- Root (feed, nonce, seq, sig, val)

	// objects

	_ Msg = &RqObject{} // <- RqO (key, prefetch)
	_ Msg = &Object{}   // -> O   (val, vals)

	// preview

	_ Msg = &RqPreview{} // -> RqPreview (feed)

	// peer exchange
	_ Msg = &RqPeers{}
	_ Msg = &Peers{}
)

//
// Msg interface, MsgCore and messages
//

// A Msg is common interface for CXO messages
type Msg interface {
	Type() Type     // type of the message to encode
	Encode() []byte // encode the message to []byte prefixed with the Type
}

//
// pings
//

// A Ping messege
type Ping struct{}

// Type implements Msg interface
func (*Ping) Type() Type { return PingType }

// Encode the Ping
func (*Ping) Encode() []byte {
	return []byte{
		byte(PingType),
	}
}

// A Pong messege
type Pong struct{}

// Type implements Msg interface
func (*Pong) Type() Type { return PongType }

// Encode the Pong
func (*Pong) Encode() []byte {
	return []byte{
		byte(PongType),
	}
}

//
// handshake
//

// A Syn is handshake initiator message
type Syn struct {
	Protocol uint16
	NodeID   cipher.PubKey // node id
}

// Type implements Msg interface
func (*Syn) Type() Type { return SynType }

// Encode the Syn
func (s *Syn) Encode() []byte { return encode(s) }

// An Ack is response for the Syn
// if handshake has been accepted.
// Otherwise, the Err returned
type Ack struct {
	NodeID cipher.PubKey // node id
}

// Type implements Msg interface
func (*Ack) Type() Type { return AckType }

// Encode the Ack
func (a *Ack) Encode() []byte { return encode(a) }

//
// common
//

// An Ok is common success reply
type Ok struct{}

// Type implements Msg interface
func (*Ok) Type() Type { return OkType }

// Encode the Ok
func (*Ok) Encode() []byte {
	return []byte{
		byte(OkType),
	}
}

// A Err is common error reply
type Err struct {
	Err string // reason
}

// Type implements Msg interface
func (*Err) Type() Type { return ErrType }

// Encode the Err
func (e *Err) Encode() []byte { return encode(e) }

//
// subscriptions
//

// A Sub message is request for subscription
type Sub struct {
	Feed cipher.PubKey
}

// Type implements Msg interface
func (*Sub) Type() Type { return SubType }

// Encode the Sub
func (s *Sub) Encode() []byte {
	return append(
		[]byte{
			byte(SubType),
		},
		s.Feed[:]...,
	)
}

// An Unsub used to notify remote peer about
// unsubscribing from a feed
type Unsub struct {
	Feed cipher.PubKey
}

// Type implements Msg interface
func (*Unsub) Type() Type { return UnsubType }

// Encode the Unsub
func (u *Unsub) Encode() []byte {
	return append(
		[]byte{
			byte(UnsubType),
		},
		u.Feed[:]...,
	)
}

//
// list of feeds
//

// A RqList is request of list of feeds
type RqList struct{}

// Type implements Msg interface
func (*RqList) Type() Type { return RqListType }

// Encode the RqList
func (*RqList) Encode() []byte {
	return []byte{
		byte(RqListType),
	}
}

// A List is reply for RqList
type List struct {
	Feeds []cipher.PubKey
}

// Type implements Msg interface
func (*List) Type() Type { return ListType }

// Encode the List
func (l *List) Encode() []byte { return encode(l) }

//
// root (push an done)
//

// A Root sent from one node to another one
// to update root object of feed described in
// Feed field of the message
type Root struct {
	Feed  cipher.PubKey // feed }
	Nonce uint64        // head } Root selector
	Seq   uint64        // seq  }

	Value []byte // encoded Root in person

	Sig cipher.Sig // signature
}

// Type implements Msg interface
func (*Root) Type() Type { return RootType }

// Encode the Root
func (r *Root) Encode() []byte { return encode(r) }

//
// objects
//

// A RqObject represents a Msg that request a data by hash
type RqObject struct {
	Key cipher.SHA256 // request
}

// Type implements Msg interface
func (*RqObject) Type() Type { return RqObjectType }

// Encode the RqObject
func (r *RqObject) Encode() []byte { return encode(r) }

// An Object reperesents encoded object
type Object struct {
	Value []byte // encoded object in person
}

// Type implements Msg interface
func (*Object) Type() Type { return ObjectType }

// Encode the Object
func (o *Object) Encode() []byte { return encode(o) }

//
// preview
//

// RqPreview is request for feeds preview
type RqPreview struct {
	Feed cipher.PubKey
}

// Type implements Msg interface
func (r *RqPreview) Type() Type { return RqPreviewType }

// Encode the RqPreview
func (r *RqPreview) Encode() []byte { return encode(r) }

//
// peer exchange
//

// RqPeers is request of peers
type RqPeers struct {
	Feed cipher.PubKey
}

// Type implements Msg interface
func (r *RqPeers) Type() Type { return RqPeersType }

// Encode the RqPeers
func (r *RqPeers) Encode() []byte { return encode(r) }

// PeerInfo contains information peer
type PeerInfo struct {
	PubKey   cipher.PubKey
	Metadata []byte
	TCPAddr  string
	UDPAddr  string
}

// Peers is reply to RqPeers
type Peers struct {
	Feed cipher.PubKey
	List []PeerInfo
}

// Type implemensts Msg interface
func (ps *Peers) Type() Type { return PeersType }

// Encode the Peers
func (ps *Peers) Encode() []byte { return encode(ps) }

//
// Type / Encode / Deocode / String()
//

// A Type represent msg prefix
type Type uint8

// Types
const (
	PingType = 1 + iota // 1
	PongType            // 2

	SynType // 3
	AckType // 4

	OkType  // 5
	ErrType // 6

	SubType   // 7
	UnsubType // 8

	RqListType // 9
	ListType   // 10

	RootType // 11

	RqObjectType // 12
	ObjectType   // 13

	RqPreviewType // 14

	RqPeersType // 15
	PeersType   // 16
)

// Type to string mapping
var msgTypeString = [...]string{
	PingType: "Ping",
	PongType: "Pong",

	SynType: "Syn",
	AckType: "Ack",

	OkType:  "Ok",
	ErrType: "Err",

	SubType:   "Sub",
	UnsubType: "Unsub",

	RqListType: "RqList",
	ListType:   "List",

	RootType: "Root",

	RqObjectType: "RqObject",
	ObjectType:   "Object",

	RqPreviewType: "RqPreview",

	RqPeersType: "RqPeers",
	PeersType:   "Peers",
}

// String implements fmt.Stringer interface
func (m Type) String() string {
	if im := int(m); im > 0 && im < len(msgTypeString) {
		return msgTypeString[im]
	}
	return fmt.Sprintf("Type<%d>", m)
}

var forwardRegistry = [...]reflect.Type{
	PingType: reflect.TypeOf(Ping{}),
	PongType: reflect.TypeOf(Pong{}),

	SynType: reflect.TypeOf(Syn{}),
	AckType: reflect.TypeOf(Ack{}),

	OkType:  reflect.TypeOf(Ok{}),
	ErrType: reflect.TypeOf(Err{}),

	SubType:   reflect.TypeOf(Sub{}),
	UnsubType: reflect.TypeOf(Unsub{}),

	RqListType: reflect.TypeOf(RqList{}),
	ListType:   reflect.TypeOf(List{}),

	RootType: reflect.TypeOf(Root{}),

	RqObjectType: reflect.TypeOf(RqObject{}),
	ObjectType:   reflect.TypeOf(Object{}),

	RqPreviewType: reflect.TypeOf(RqPreview{}),

	RqPeersType: reflect.TypeOf(RqPeers{}),
	PeersType:   reflect.TypeOf(Peers{}),
}

// An InvalidTypeError represents decoding error when
// incoming message is malformed and its type invalid
type InvalidTypeError struct {
	typ Type
}

// Type return Type which cause the error
func (e InvalidTypeError) Type() Type {
	return e.typ
}

// Error implements builting error interface
func (e InvalidTypeError) Error() string {
	return fmt.Sprint("invalid message type: ", e.typ.String())
}

var (
	// ErrEmptyMessage occurs when you
	// try to Decode an empty slice
	ErrEmptyMessage = errors.New("empty message")
	// ErrIncomplieDecoding occurs when incoming message
	// decoded correctly but the decoding doesn't use
	// entire encoded message
	ErrIncomplieDecoding = errors.New("incomplete decoding")
)

// encode given message to []byte prefixed by Type
func encode(msg Msg) (p []byte) {
	p = append(
		[]byte{
			byte(msg.Type()),
		},
		encoder.Serialize(msg)...,
	)
	return
}

// Decode encoded Type-prefixed data to message.
// It can returns encoding errors or InvalidTypeError
func Decode(p []byte) (msg Msg, err error) {

	if len(p) < 1 {
		err = ErrEmptyMessage
		return
	}

	var mt = Type(p[0])

	if mt <= 0 || int(mt) >= len(forwardRegistry) {
		err = InvalidTypeError{mt}
		return
	}

	var (
		typ = forwardRegistry[mt]
		val = reflect.New(typ)

		n uint64
	)

	if n, err = encoder.DeserializeRawToValue(p[1:], val); err != nil {
		return
	}

	if n+1 != uint64(len(p)) {
		err = ErrIncomplieDecoding
		return
	}

	msg = val.Interface().(Msg)
	return
}
