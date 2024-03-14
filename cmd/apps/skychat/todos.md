
# ToDos and brain-storming about the future of skychat

## General

- TODO: message statuses: sent, received(achieved by implementing the peer to send a 'received-message' back) to check whether the message really was received by the peer
- [x] database for user repository
- [x] database for visor repository
- FUTUREFEATUE: flags to setup if database or in-memory is used for repositories (data is lost when stoping app)
    --> maybe even a way on setting this up for each chat, so simultaneously persistent chats and "deleteable" chats are possible
    --> how about "self-deleting-messages"? deletes itself after (1hour, 24hour, etc...)
- [x] cli-interface
    with cli-interface a connection with the systray app would be possible -> notifications about new messages, incoming calls etc.
- FUTUREFEATUE: encrypted messages (encrypted sending and encrypted saving on local storage) --> Password for app required

## General-Future

- FUTUREFEATURE: voip-channels
- FUTUREFEATURE: video-streams
- FUTUREFEATURE: sending-fiber via chat, with notification about received payments -> also sending-fiber-payment-requests
- FUTUREFEATURE: Implement Interface to skycoin/fiber wallet to send and receive crypto inside the chat. (Also so open to use other wallets than fiber wallets? Or just implement other coins into fiberwallet)

## UI

- TODO:Make UI more beautiful
- TODO:Implement to look into peer-book and set custom aliases
- TODO:Implement to look into server-info, and see all lists (admins, members, etc)

How about a way on customizing the appearance of a group chat and saving the css-data inside the server and sending it to the peers?

## Usecases

### P2P

- make p2p room within own visor -> for informations from other apps or so

### Server & Rooms

- TODO:send_hire_admin_message.go 		//maybe only allow this action from server-host
- TODO:send_fire_admin_message.go		//maybe only allow this action from server-host
[x]send_hire_moderator_message.go
[x]send_fire_moderator_message.go
[x]send_mute_peer_message.go 			--> backend implemented, frontend missing
[x]:send_unmute_peer_message.go		--> backend implemented, frontend missing

- TODO:send_add_room_message.go
- TODO:send_delete_room_message.go
- TODO:send_hide_room_message.go
- TODO:send_unhide_room_message.go
- TODO:send_set_route

- []send_hide_message.go				//first a way to edit messages must be implemented
- []send_unhide_message.go			//first a way to edit messages must be implemented
- []send_edit_message.go				//first a way to edit messages must be implemented
- []send_delete_message.go			//first a way to edit messages must be implemented

- []send_invite_message.go			//an option to send an invite message to a group which then can be accepted or rejected by the peer

- []transfer_local_server.go			//a way to transfer ownership of a room or server to another visor

- []implement DNS handles for Servers/Rooms and P2Ps
