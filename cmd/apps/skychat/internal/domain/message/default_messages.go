package message

import (
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

// getUTCTimeStamp returns UTC TimeStamp
// This is used so local time of sender is unknown to receiver
func getUTCTimeStamp() time.Time {
	loc, err := time.LoadLocation("UTC")
	if err != nil {
		return time.Now()
	}
	now := time.Now().In(loc)
	return now
}

// NewTextMessage returns a Message
func NewTextMessage(pkOrigin cipher.PubKey, routeDestination util.PKRoute, msg []byte) Message {
	m := Message{}
	m.Origin = pkOrigin
	m.Root = util.NewP2PRoute(pkOrigin)
	m.Dest = routeDestination
	m.MsgType = TxtMsgType
	m.MsgSubtype = 0
	m.Message = msg
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewRouteRequestMessage returns a request Message
func NewRouteRequestMessage(pkOrigin cipher.PubKey, routeDestination util.PKRoute) Message {
	m := Message{}
	m.Origin = pkOrigin
	m.Root = util.NewP2PRoute(pkOrigin)
	m.Dest = routeDestination
	m.MsgType = ConnMsgType
	m.MsgSubtype = ConnMsgTypeRequest
	m.Message = []byte("Chat Request")
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewChatAcceptMessage returns a chat accepted message
// pk is the users pk to set the messages root
func NewChatAcceptMessage(root util.PKRoute, dest util.PKRoute) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = ConnMsgType
	m.MsgSubtype = ConnMsgTypeAccept
	m.Status = MsgStatusInitial
	m.Message = []byte("Chat Accepted")
	m.Time = getUTCTimeStamp()
	return m
}

// NewChatRejectMessage returns new chat rejected message
func NewChatRejectMessage(root util.PKRoute, dest util.PKRoute) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = ConnMsgType
	m.MsgSubtype = ConnMsgTypeReject
	m.Message = []byte("Chat Rejected")
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewChatLeaveMessage returns new chat leave message
func NewChatLeaveMessage(root util.PKRoute, dest util.PKRoute) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = ConnMsgType
	m.MsgSubtype = ConnMsgTypeLeave
	m.Message = []byte("Chat Left")
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewRouteDeletedMessage returns new message to info about deleted route
func NewRouteDeletedMessage(root util.PKRoute, dest util.PKRoute) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = ConnMsgType
	m.MsgSubtype = ConnMsgTypeDelete
	m.Message = []byte("Chat Deleted")
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewChatInfoMessage returns new chat info
func NewChatInfoMessage(root util.PKRoute, dest util.PKRoute, info []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = InfoMsgType
	m.MsgSubtype = InfoMsgTypeSingle
	m.Message = info
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewAddRoomMessage returns a Message
func NewAddRoomMessage(root util.PKRoute, dest util.PKRoute, info []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = CmdMsgType
	m.MsgSubtype = CmdMsgTypeAddRoom
	m.Message = info
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewDeleteRoomMessage returns a Message
func NewDeleteRoomMessage(root util.PKRoute, dest util.PKRoute) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = CmdMsgType
	m.MsgSubtype = CmdMsgTypeDeleteRoom
	m.Message = []byte("Room Deleted")
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewRoomMembersMessage returns a Message of room members
func NewRoomMembersMessage(root util.PKRoute, dest util.PKRoute, members []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = InfoMsgType
	m.MsgSubtype = InfoMsgTypeRoomMembers
	m.Message = members
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewRoomModsMessage returns a Message of room moderators
func NewRoomModsMessage(root util.PKRoute, dest util.PKRoute, moderators []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = InfoMsgType
	m.MsgSubtype = InfoMsgTypeRoomMembers
	m.Message = moderators
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewRoomMutedMessage returns a Message of muted pks of room
func NewRoomMutedMessage(root util.PKRoute, dest util.PKRoute, muted []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = InfoMsgType
	m.MsgSubtype = InfoMsgTypeRoomMuted
	m.Message = muted
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewMutePeerMessage returns a Message to mute a peer
func NewMutePeerMessage(root util.PKRoute, dest util.PKRoute, pk []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = CmdMsgType
	m.MsgSubtype = CmdMsgTypeMutePeer
	m.Message = pk
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewUnmutePeerMessage returns a Message to mute a peer
func NewUnmutePeerMessage(root util.PKRoute, dest util.PKRoute, pk []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = CmdMsgType
	m.MsgSubtype = CmdMsgTypeUnmutePeer
	m.Message = pk
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewHireModeratorMessage returns a Message to hire a peer as moderator
func NewHireModeratorMessage(root util.PKRoute, dest util.PKRoute, pk []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = CmdMsgType
	m.MsgSubtype = CmdMsgTypeHireModerator
	m.Message = pk
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}

// NewFireModeratorMessage returns a Message to fire a moderator
func NewFireModeratorMessage(root util.PKRoute, dest util.PKRoute, pk []byte) Message {
	m := Message{}
	m.Origin = root.Visor
	m.Root = root
	m.Dest = dest
	m.MsgType = CmdMsgType
	m.MsgSubtype = CmdMsgTypeFireModerator
	m.Message = pk
	m.Status = MsgStatusInitial
	m.Time = getUTCTimeStamp()
	return m
}
