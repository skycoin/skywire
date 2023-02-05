package commands

//-to-dos and brain-storming about the future of skychat

//general
//TODO: database for user and visor repository
//TODO: flags to setup if database or in-memory is used for repositories (data is lost when stoping app)
//--> maybe even a way on setting this up for each chat, so simultaneously persistent chats and "deleteable" chats are possible
//--> how about "self-deleting-messages"? deletes itself after (1hour, 24hour, etc...)
//TODO: cli-interface
//with cli-interface a connection with the systray app would be possible -> notifications about new messages, incoming calls etc.
//TODO: encrypted messages (encrypted sending and encrypted saving on local storage) --> Password for app required

//general-future
//TODO: voip-channels
//TODO: video-streams
//TODO: sending-fiber via chat, with notification about received payments -> also sending-fiber-requests

//UI
//TODO:Make UI more beautiful
//TODO:Implement to look into peer-book and set custom aliases
//TODO:Implement to look into server-info, and see all lists (admins, members, etc)

//? How about a way on customizing the appearance of a group chat and saving the css-data inside the server and sending it to the peers

//usecases--------------------------------------------

//p2p-----------------------

//server & rooms------------
//TODO:send_hire_admin_message.go 		//maybe only allow this action from server-host
//TODO:send_fire_admin_message.go		//maybe only allow this action from server-host
//TODO:send_hire_moderator_message.go
//TODO:send_fire_moderator_message.go
//TODO:send_mute_peer_message.go
//TODO:send_unmute_peer_message.go

//TODO:send_add_room_message.go
//TODO:send_delete_room_message.go
//TODO:send_hide_room_message.go
//TODO:send_unhide_room_message.go
//TODO:send_set_route_info_message.go

//send_hide_message.go				//first a way to edit messages must be implemented
//send_unhide_message.go			//first a way to edit messages must be implemented
//send_edit_message.go				//first a way to edit messages must be implemented
//send_delete_message.go			//first a way to edit messages must be implemented

//send_invite_message.go			//an option to send an invite message to a group which then can be accepted or rejected by the peer

//transfer_local_server.go			//a way to transfer ownership to another visor
