
/////////////////////////////////////////////////////////////
//// Classes
/////////////////////////////////////////////////////////////

class Info{
    constructor(pk,alias,desc,img){
      this.pk = pk;
      this.alias = alias;
      this.desc = desc;
      this.img = img;
    }
  }

  class Settings{
    constructor(blacklist){
      this.blacklist = blacklist;
    }
  }

  class User{
    constructor(info,settings,peerbook){
      this.info = info;
      this.settings = settings;
      this.peerbook = peerbook;

      this.inSettings = false;
    }
  }

  class Peer{
    constructor(info,alias){
      this.info = info;
      this.alias = alias;
    }
  }

  class Peerbook{
      constructor(peers){
        this.peers = peers
      }
  }

  class Message{
    constructor(id,origin,ts,root,dest,type,subtype,message,status,seen){
      this.id = id;
      this.origin = origin;
      this.ts = ts;
      this.root = root;
      this.dest = dest;
      this.type = type;
      this.subtype = subtype;
      this.message = message;
      this.status = status;
      this.seen = seen;
    }
  }

  class Route{
    constructor(visor,server,room){
      this.visor = visor;
      this.server = server;
      this.room = room;
    }
  }

  class Room{
    constructor(route,info,messages,isVisible,type,members,mods,muted,blacklist,whitelist){
      this.pk = route.room;
      this.route = route;
      this.info = info;
      this.messages = messages;
      this.isVisible = isVisible;
      this.type = type;
      this.members = members;
      this.mods = mods;
      this.muted = muted;
      this.blacklist = blacklist;
      this.whitelist = whitelist;
    }
  }

  class Server{
    constructor(route,info,members,admins,muted,blacklist,whitelist,rooms){
      this.route = route;
      this.info = info;
      this.members = members;
      this.admins = admins;
      this.muted = muted;
      this.blacklist = blacklist;
      this.whitelist = whitelist;
      this.rooms = rooms;
    }
  }

  class Visor{
    constructor(pk,p2p,server){
      this.pk = pk,
      this.p2p = p2p;
      this.server = server;
    }
  }

/**Dummy Data */
//Default img
Img = "iVBORw0KGgoAAAANSUhEUgAAAPoAAAD6CAIAAAAHjs1qAAAAGXRFWHRTb2Z0d2FyZQBBZG9iZSBJbWFnZVJlYWR5ccllPAAAAyZpVFh0WE1MOmNvbS5hZG9iZS54bXAAAAAAADw/eHBhY2tldCBiZWdpbj0i77u/IiBpZD0iVzVNME1wQ2VoaUh6cmVTek5UY3prYzlkIj8+IDx4OnhtcG1ldGEgeG1sbnM6eD0iYWRvYmU6bnM6bWV0YS8iIHg6eG1wdGs9IkFkb2JlIFhNUCBDb3JlIDUuNS1jMDIxIDc5LjE1NTc3MiwgMjAxNC8wMS8xMy0xOTo0NDowMCAgICAgICAgIj4gPHJkZjpSREYgeG1sbnM6cmRmPSJodHRwOi8vd3d3LnczLm9yZy8xOTk5LzAyLzIyLXJkZi1zeW50YXgtbnMjIj4gPHJkZjpEZXNjcmlwdGlvbiByZGY6YWJvdXQ9IiIgeG1sbnM6eG1wPSJodHRwOi8vbnMuYWRvYmUuY29tL3hhcC8xLjAvIiB4bWxuczp4bXBNTT0iaHR0cDovL25zLmFkb2JlLmNvbS94YXAvMS4wL21tLyIgeG1sbnM6c3RSZWY9Imh0dHA6Ly9ucy5hZG9iZS5jb20veGFwLzEuMC9zVHlwZS9SZXNvdXJjZVJlZiMiIHhtcDpDcmVhdG9yVG9vbD0iQWRvYmUgUGhvdG9zaG9wIENDIDIwMTQgKFdpbmRvd3MpIiB4bXBNTTpJbnN0YW5jZUlEPSJ4bXAuaWlkOjY5OTMyNUUzOTY0QjExRUI4MDZERkQ5M0JBOUY1NThGIiB4bXBNTTpEb2N1bWVudElEPSJ4bXAuZGlkOjY5OTMyNUU0OTY0QjExRUI4MDZERkQ5M0JBOUY1NThGIj4gPHhtcE1NOkRlcml2ZWRGcm9tIHN0UmVmOmluc3RhbmNlSUQ9InhtcC5paWQ6Njk5MzI1RTE5NjRCMTFFQjgwNkRGRDkzQkE5RjU1OEYiIHN0UmVmOmRvY3VtZW50SUQ9InhtcC5kaWQ6Njk5MzI1RTI5NjRCMTFFQjgwNkRGRDkzQkE5RjU1OEYiLz4gPC9yZGY6RGVzY3JpcHRpb24+IDwvcmRmOlJERj4gPC94OnhtcG1ldGE+IDw/eHBhY2tldCBlbmQ9InIiPz7FIRCzAAAPn0lEQVR42uyd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"
//Types of Messages
ConnMsgType = 1
TxtMsgType = 2
InfoMsgType = 3
CmdMsgType = 4

//subtypes of ConnMsgType
ConnMsgTypeRequest = 1
ConnMsgTypeAccept = 2
ConnMsgTypeReject = 3

//Types of MessageStatus
MsgStatusSent = 1
MsgStatusReceived = 2

//Types of Rooms
ChatRoomType = 1
BoardRoomType = 2

//Peer1 dummy
dummyPeer1PK = "12345678901"
dummyPeer1Alias = "Dummy-Peer1-Alias"
dummyPeer1Description = "Dummy-Peer1-Description"
dummyPeer1Info = new Info(dummyPeer1PK,dummyPeer1Alias,dummyPeer1Description,Img)
dummyPeer1CustomAlias ="Dummy-Peer1-Custom-Alias"
dummyPeer1 = new Peer(dummyPeer1Info,dummyPeer1CustomAlias)
//Peer2 dummy
dummyPeer2PK = "12345678902"
dummyPeer2Alias = "Dummy-Peer2-Alias"
dummyPeer2Description = "Dummy-Peer2-Description"
dummyPeer2Info = new Info(dummyPeer2PK,dummyPeer2Alias,dummyPeer2Description,Img)
dummyPeer2CustomAlias ="Dummy-Peer2-Custom-Alias"
dummyPeer2 = new Peer(dummyPeer2Info,dummyPeer2CustomAlias)
//Peer3 dummy
dummyPeer3PK = "12345678903"
dummyPeer3Alias = "Dummy-Peer3-Alias"
dummyPeer3Description = "Dummy-Peer3-Description"
dummyPeer3Info = new Info(dummyPeer3PK,dummyPeer3Alias,dummyPeer3Description,Img)
dummyPeer3CustomAlias ="Dummy-Peer3-Custom-Alias"
dummyPeer3 = new Peer(dummyPeer3Info,dummyPeer3CustomAlias)
//User dummy
dummyUserPK = "12345678900"
dummyUserAlias = "Dummy-User-Alias"
dummyUserDescription = "Dummy-User-Description"
dummyUserInfo = new Info(dummyUserPK,dummyUserAlias,dummyUserDescription,Img)
dummyUserSettings = new Settings("")
dummyUserPeerbook = new Peerbook([dummyPeer1,dummyPeer2])
dummyUser = new User(dummyUserInfo,dummyUserSettings,dummyUserPeerbook)

/******************************* */
//GroupChat dummyChat-local
/******************************* */
///--Route
dummyChatGroupLocal1Visor = dummyUserPK
dummyChatGroupLocal1Server = "1"
dummyChatGroupLocal1Room = "1"
dummyChatGroupLocal1Route = new Route(dummyChatGroupLocal1Visor,dummyChatGroupLocal1Server,dummyChatGroupLocal1Room)
///--Info
dummyChatGroupLocal1PK = dummyChatGroupLocal1Room
dummyChatGroupLocal1Alias = "Dummy-ChatGroup-Local1-Alias"
dummyChatGroupLocal1Description = "Dummy-ChatGroup-Local1-Description"
dummyChatGroupLocal1Info = new Info(dummyChatGroupLocal1PK,dummyChatGroupLocal1Alias,dummyChatGroupLocal1Description,Img)
///--Messages
///---Message 1
dummyChatGroupLocal1Message1Id = 1
dummyChatGroupLocal1Message1Origin = dummyPeer1PK
dummyChatGroupLocal1Message1Ts = "2022-11-29 19:03:01"
dummyChatGroupLocal1Message1Root = new Route(dummyPeer1PK,dummyPeer1PK,dummyPeer1PK)
dummyChatGroupLocal1Message1Dest = new Route(dummyChatGroupLocal1Visor,dummyChatGroupLocal1Server,dummyChatGroupLocal1Room)
dummyChatGroupLocal1Message1Type = TxtMsgType
dummyChatGroupLocal1Message1Subtype = 0
dummyChatGroupLocal1Message1Message = "Hello I am Peer 1"
dummyChatGroupLocal1Message1Status = MsgStatusReceived
dummyChatGroupLocal1Message1Seen = true
dummyChatGroupLocal1Message1 = new Message(dummyChatGroupLocal1Message1Id,dummyChatGroupLocal1Message1Origin,dummyChatGroupLocal1Message1Ts,dummyChatGroupLocal1Message1Root,dummyChatGroupLocal1Message1Dest,dummyChatGroupLocal1Message1Type,dummyChatGroupLocal1Message1Subtype,dummyChatGroupLocal1Message1Message,dummyChatGroupLocal1Message1Status,dummyChatGroupLocal1Message1Seen)
///---Message 2
dummyChatGroupLocal1Message2Id = 2
dummyChatGroupLocal1Message2Origin = dummyPeer2PK
dummyChatGroupLocal1Message2Ts = "2022-11-29 19:04:01"
dummyChatGroupLocal1Message2Root = new Route(dummyPeer2PK,dummyPeer2PK,dummyPeer2PK)
dummyChatGroupLocal1Message2Dest = new Route(dummyChatGroupLocal1Visor,dummyChatGroupLocal1Server,dummyChatGroupLocal1Room)
dummyChatGroupLocal1Message2Type = TxtMsgType
dummyChatGroupLocal1Message2Subtype = 0
dummyChatGroupLocal1Message2Message = "Hello I am Peer 2"
dummyChatGroupLocal1Message2Status = MsgStatusReceived
dummyChatGroupLocal1Message2Seen = true
dummyChatGroupLocal1Message2 = new Message(dummyChatGroupLocal1Message2Id,dummyChatGroupLocal1Message2Origin,dummyChatGroupLocal1Message2Ts,dummyChatGroupLocal1Message2Root,dummyChatGroupLocal1Message2Dest,dummyChatGroupLocal1Message2Type,dummyChatGroupLocal1Message2Subtype,dummyChatGroupLocal1Message2Message,dummyChatGroupLocal1Message2Status,dummyChatGroupLocal1Message2Seen)
///---Message 3
dummyChatGroupLocal1Message3Id = 3
dummyChatGroupLocal1Message3Origin = dummyUserPK
dummyChatGroupLocal1Message3Ts = "2022-11-29 19:05:01"
dummyChatGroupLocal1Message3Root = new Route(dummyUserPK,dummyUserPK,dummyUserPK)
dummyChatGroupLocal1Message3Dest = new Route(dummyChatGroupLocal1Visor,dummyChatGroupLocal1Server,dummyChatGroupLocal1Room)
dummyChatGroupLocal1Message3Type = TxtMsgType
dummyChatGroupLocal1Message3Subtype = 0
dummyChatGroupLocal1Message3Message = "Hello I am the User"
dummyChatGroupLocal1Message3Status = MsgStatusReceived
dummyChatGroupLocal1Message3Seen = true
dummyChatGroupLocal1Message3 = new Message(dummyChatGroupLocal1Message3Id,dummyChatGroupLocal1Message3Origin,dummyChatGroupLocal1Message3Ts,dummyChatGroupLocal1Message3Root,dummyChatGroupLocal1Message3Dest,dummyChatGroupLocal1Message3Type,dummyChatGroupLocal1Message3Subtype,dummyChatGroupLocal1Message3Message,dummyChatGroupLocal1Message3Status,dummyChatGroupLocal1Message3Seen)
///---Message 4
dummyChatGroupLocal1Message4Id = 3
dummyChatGroupLocal1Message4Origin = dummyPeer3PK
dummyChatGroupLocal1Message4Ts = "2022-11-29 19:06:01"
dummyChatGroupLocal1Message4Root = new Route(dummyPeer3PK,dummyPeer3PK,dummyPeer3PK)
dummyChatGroupLocal1Message4Dest = new Route(dummyChatGroupLocal1Visor,dummyChatGroupLocal1Server,dummyChatGroupLocal1Room)
dummyChatGroupLocal1Message4Type = TxtMsgType
dummyChatGroupLocal1Message4Subtype = 0
dummyChatGroupLocal1Message4Message = "Hello I am Peer 3"
dummyChatGroupLocal1Message4Status = MsgStatusReceived
dummyChatGroupLocal1Message4Seen = true
dummyChatGroupLocal1Message4 = new Message(dummyChatGroupLocal1Message4Id,dummyChatGroupLocal1Message4Origin,dummyChatGroupLocal1Message4Ts,dummyChatGroupLocal1Message4Root,dummyChatGroupLocal1Message4Dest,dummyChatGroupLocal1Message4Type,dummyChatGroupLocal1Message4Subtype,dummyChatGroupLocal1Message4Message,dummyChatGroupLocal1Message4Status,dummyChatGroupLocal1Message4Seen)


dummyChatGroupLocal1Messages = [dummyChatGroupLocal1Message1,dummyChatGroupLocal1Message2,dummyChatGroupLocal1Message3,dummyChatGroupLocal1Message4]
///--isVisible
dummyChatGroupLocal1IsVisible = false
///--Type
dummyChatGroupLocal1Type = ChatRoomType
///--Members
dummyChatGroupLocal1Members = [dummyUser,dummyPeer1,dummyPeer2,dummyPeer3]
///--Room
dummyChatGroupLocal1 = new Room(dummyChatGroupLocal1Route,dummyChatGroupLocal1Info,dummyChatGroupLocal1Messages,dummyChatGroupLocal1IsVisible,dummyChatGroupLocal1Type,dummyChatGroupLocal1Members,null,null,null,null)
//TODO:dummyChatGroup


//Server dummyChat-local
//TODO:dummyChatServer

/******************************* */
//P2P dummyChat-remote (Peer1)
/******************************* */
///--Route
dummyChatP2P1Visor = dummyPeer1PK
dummyChatP2P1Server = dummyPeer1PK
dummyChatP2P1Room = dummyPeer1PK
dummyChatP2P1Route = new Route(dummyChatP2P1Visor,dummyChatP2P1Server,dummyChatP2P1Room)
///--Info
dummyChatP2P1PK = dummyPeer1PK
dummyChatP2P1Alias = "Dummy-ChatP2P-Remote1-Alias"
dummyChatP2P1Description = "Dummy-ChatP2P-Remote1-Description"
dummyChatP2P1Info = new Info(dummyChatP2P1PK,dummyChatP2P1Alias,dummyChatP2P1Description,Img)
///--Messages
///---Message 1
dummyChatP2P1Message1Id = 1
dummyChatP2P1Message1Origin = dummyPeer1PK
dummyChatP2P1Message1Ts = "2022-11-29 19:03:01"
dummyChatP2P1Message1Root = new Route(dummyPeer1PK,dummyPeer1PK,dummyPeer1PK)
dummyChatP2P1Message1Dest = new Route(dummyUserPK,dummyUserPK,dummyUserPK)
dummyChatP2P1Message1Type = TxtMsgType
dummyChatP2P1Message1Subtype = 0
dummyChatP2P1Message1Message = "Hello"
dummyChatP2P1Message1Status = MsgStatusReceived
dummyChatP2P1Message1Seen = true
dummyChatP2P1Message1 = new Message(dummyChatP2P1Message1Id,dummyChatP2P1Message1Origin,dummyChatP2P1Message1Ts,dummyChatP2P1Message1Root,dummyChatP2P1Message1Dest,dummyChatP2P1Message1Type,dummyChatP2P1Message1Subtype,dummyChatP2P1Message1Message,dummyChatP2P1Message1Status,dummyChatP2P1Message1Seen)
///---Message 2
dummyChatP2P1Message2Id = 2
dummyChatP2P1Message2Origin = dummyUserPK
dummyChatP2P1Message2Ts = "2022-11-29 19:05:01"
dummyChatP2P1Message2Root = new Route(dummyUserPK,dummyUserPK,dummyUserPK)
dummyChatP2P1Message2Dest = new Route(dummyPeer1PK,dummyPeer1PK,dummyPeer1PK)
dummyChatP2P1Message2Type = TxtMsgType
dummyChatP2P1Message2Subtype = 0
dummyChatP2P1Message2Message = "Hello Back"
dummyChatP2P1Message2Status = MsgStatusSent
dummyChatP2P1Message2Seen = true
dummyChatP2P1Message2 = new Message(dummyChatP2P1Message2Id,dummyChatP2P1Message2Origin,dummyChatP2P1Message2Ts,dummyChatP2P1Message2Root,dummyChatP2P1Message2Dest,dummyChatP2P1Message2Type,dummyChatP2P1Message2Subtype,dummyChatP2P1Message2Message,dummyChatP2P1Message2Status,dummyChatP2P1Message2Seen)


dummyChatP2P1Messages = [dummyChatP2P1Message1,dummyChatP2P1Message2]
///--isVisible
dummyChatP2P1IsVisible = true
///--Type
dummyChatP2P1Type = ChatRoomType
///--Room
dummyChatP2P1 = new Room(dummyChatP2P1Route,dummyChatP2P1Info,dummyChatP2P1Messages,dummyChatP2P1IsVisible,dummyChatP2P1Type,null,null,null,null,null)

/******************************* */
//P2P dummyChat-remote (Peer2)
/******************************* */
///--Route
dummyChatP2P2Visor = dummyPeer2PK
dummyChatP2P2Server = dummyPeer2PK
dummyChatP2P2Room = dummyPeer2PK
dummyChatP2P2Route = new Route(dummyChatP2P2Visor,dummyChatP2P2Server,dummyChatP2P2Room)
///--Info
dummyChatP2P2PK = dummyPeer2PK
dummyChatP2P2Alias = "Dummy-ChatP2P-Remote2-Alias"
dummyChatP2P2Description = "Dummy-ChatP2P-Remote1-Description"
dummyChatP2P2Info = new Info(dummyChatP2P2PK,dummyChatP2P2Alias,dummyChatP2P2Description,Img)
///--Messages
///---Message 1
dummyChatP2P2Message1Id = 1
dummyChatP2P2Message1Origin = dummyPeer2PK
dummyChatP2P2Message1Ts = "2022-11-29 20:03:00"
dummyChatP2P2Message1Root = new Route(dummyPeer2PK,dummyPeer2PK,dummyPeer2PK)
dummyChatP2P2Message1Dest = new Route(dummyUserPK,dummyUserPK,dummyUserPK)
dummyChatP2P2Message1Type = TxtMsgType
dummyChatP2P2Message1Subtype = 0
dummyChatP2P2Message1Message = "Hello User"
dummyChatP2P2Message1Status = MsgStatusReceived
dummyChatP2P2Message1Seen = true
dummyChatP2P2Message1 = new Message(dummyChatP2P2Message1Id,dummyChatP2P2Message1Origin,dummyChatP2P2Message1Ts,dummyChatP2P2Message1Root,dummyChatP2P2Message1Dest,dummyChatP2P2Message1Type,dummyChatP2P2Message1Subtype,dummyChatP2P2Message1Message,dummyChatP2P2Message1Status,dummyChatP2P2Message1Seen)
///---Message 2
dummyChatP2P2Message2Id = 2
dummyChatP2P2Message2Origin = dummyUserPK
dummyChatP2P2Message2Ts = "2022-11-29 20:05:01"
dummyChatP2P2Message2Root = new Route(dummyUserPK,dummyUserPK,dummyUserPK)
dummyChatP2P2Message2Dest = new Route(dummyPeer2PK,dummyPeer2PK,dummyPeer2PK)
dummyChatP2P2Message2Type = TxtMsgType
dummyChatP2P2Message2Subtype = 0
dummyChatP2P2Message2Message = "Hello Peer2"
dummyChatP2P2Message2Status = MsgStatusSent
dummyChatP2P2Message2Seen = false
dummyChatP2P2Message2 = new Message(dummyChatP2P2Message2Id,dummyChatP2P2Message2Origin,dummyChatP2P2Message2Ts,dummyChatP2P2Message2Root,dummyChatP2P2Message2Dest,dummyChatP2P2Message2Type,dummyChatP2P2Message2Subtype,dummyChatP2P2Message2Message,dummyChatP2P2Message2Status,dummyChatP2P2Message2Seen)


dummyChatP2P2Messages = [dummyChatP2P2Message1,dummyChatP2P2Message2]
///--isVisible
dummyChatP2P2IsVisible = true
///--Type
dummyChatP2P2Type = ChatRoomType
///--Room
dummyChatP2P2 = new Room(dummyChatP2P2Route,dummyChatP2P2Info,dummyChatP2P2Messages,dummyChatP2P2IsVisible,dummyChatP2P2Type,null,null,null,null,null)


//GroupChat dummyChat-peer
//TODO:
//Server dummyChat-peer
//TODO:

/**End Dummy Data */

/////////////////////////////////////////////////////////////
//// Skychat
/////////////////////////////////////////////////////////////
  class Skychat {
    constructor() {}

    fetchData(test){
      if (!test) {
      return Promise.all([this.getUserInfo(),
              this.getUserSettings(),
              this.getChatAll(),
              this.getWebsocketPort()
            ]).then(([userInfo,userSettings,chats,port]) => {
              this.user = new User(userInfo,userSettings);
              this.chats = chats
              this.port = port
              return this.init()
            });
          }
      else {
        return new Promise((resolve,reject)=>{
          setTimeout(() => { 
            resolve(10);
          }, 1*100)
        }).then(()=> {
          this.user = dummyUser
          this.chats = [dummyChatP2P1,dummyChatP2P2,dummyChatGroupLocal1];
          return this.init()
        })
        
      }
    }
   
    init(){
      this.addUser(this.user);
      this.addAddLocal(this.user);
      this.addAddRemote(this.user);

      this.chat = null;
      if (this.chats != null){
        this.chats.forEach(r => this._addChat(r));
      }

      this.notificationsSubscribe(this.port);
      return this;
    }

/////////////////////////////////////////////////////////////
//// UI specific functions
/////////////////////////////////////////////////////////////

    addUser(c) {
      document.getElementById('user').innerHTML +=
        `<a href="#" class="${c.inSettings ? 'active' : ''} " onclick="app._showSettings(); return false;">
          <img class="small-profile-picture" src="data:image/png;base64,${c.info.img}" />
          <div class="text-container">
            <div class="alias">
              ${c.info.alias}
            </div>
            <div class="pk">
              ${c.info.pk}
            </div>
          </div>
        </a>`;

    }

    addAddLocal(c){
      document.getElementById('addLocal').innerHTML +=
        `<a href="#" class="${c.inAddLocal ? 'active' : ''} " onclick="app._showAddLocal(); return false;">
        <div>Add Local Server and Rooms</div>
        </a>`;
    }

    addAddRemote(c){
      document.getElementById('joinRemote').innerHTML +=
        `<a href="#" class="${c.inJoinRemote ? 'active' : ''} " onclick="app._showAddRemote(); return false;">
        <div>Join Remote Server and Rooms</div>
        </a>`;
    }

    _updateUser(c) {
      this.user = c

      document.getElementById('user').innerHTML =
        `<a href="#" class="${c.inSettings ? 'active' : ''} " onclick="app._showSettings(); return false;">
          <img class="small-profile-picture" src="data:image/png;base64,${c.info.img}" />
          <div class="text-container">
            <div class="alias">
              ${c.info.alias}
            </div>
            <div class="pk">
              ${c.info.pk}
            </div>
          </div>
        </a>`;
    }

    _getRoomPrefix(r){
      let prefix = ""
      if (r.route.visor == r.route.room) {
        //P2P
        prefix = "P2P"
      } else {
        //GroupChat / Room
        prefix = "G"
      }

      if (r.route.visor == this.user.info.pk){
        //Hosted on localhost
        prefix += String.fromCharCode(0x2302)
      }
      return prefix
    }

    _addChat(r) {
      if (!this.chats.includes(r)) {
        this.chats.push(r);
      }


      let lastMsg = this.getLastMessageFromRoute(r.route)

      let prefix = this._getRoomPrefix(r)

      document.getElementById('chatList').innerHTML +=
        `<div id="chat_${r.pk}">
          <li><a href="#" class="${r.pk === this.chat ? 'active' : ''} destination" onclick="app._selectChat('${r.pk}'); return false;">
            <img class="small-profile-picture" src="data:image/png;base64,${r.info.img}" />
            <div class="text-container">
              <div class="alias">
                ${prefix} ${r.info.alias}
              </div>
              <div class="pk">
                ${r.pk}
              </div>
              <div class="msg">
                ${lastMsg}
              </div>
            </div>
            <!-- <div>
              <div class="unreaded">
                0
              </div>
            </div> -->
          </a></li></div>`;

    }

    _updateChat(r) {
      var index = this.getChatIndexFromRoute(r.route)
      this.chats[index] = r

      let lastMsg = this.getLastMessageFromRoute(r.route)

      let prefix = this._getRoomPrefix(r)

      document.getElementById('chat_'+ r.pk).innerHTML =
        `<li><a href="#" class="${r.pk === this.chat ? 'active' : ''} destination" onclick="app._selectChat('${r.pk}'); return false;">
          <img class="small-profile-picture" src="data:image/png;base64,${r.info.img}" />
          <div class="text-container">
            <div class="alias">
              ${prefix} ${r.info.alias}
            </div>
            <div class="pk">
              ${r.pk}
            </div>
            <div class="msg">
              ${lastMsg}
            </div>
          </div>
          <!-- <div>
            <div class="unreaded">
              0
            </div>
          </div> -->
        </a></li>`;

      if (this.chat = r.pk){
        document.getElementById('messages').innerHTML = '';
        this.getMessagesFromRoute(r.route).forEach(msg => this._showMessage(msg,r.route));
        document.getElementById('chatButtonsContainer').classList.remove('hidden');
        document.getElementById('msgForm').classList.remove('hidden');
        document.getElementById('msgField').focus();

        let msgArea = document.getElementById('messages');
        msgArea.scrollTop = msgArea.scrollHeight;
      }
    }


    _showSettings(){
      this.user.inSettings = true;

      let info = this.user.info;
      let settings = this.user.settings;
      this.chat = null

      //unselect chats in sidebar
      document.querySelectorAll('.destination').forEach(item => 
      {    
        item.classList.remove('active');
      });

      document.getElementById('form').innerHTML = 
      ``;
      //empty messages
      document.getElementById('messages').innerHTML = '';
      //empty header
      document.getElementById('chatHeaderInformation').innerHTML = ''
      //hide send message bar and button
      document.getElementById('chatButtonsContainer').classList.add('hidden');
      document.getElementById('msgForm').classList.add('hidden');

      document.getElementById('form').innerHTML += 
      `<div id="settingsContainer" class="settings-container" style="z-index:100; position: fixed;">
        <div class="settings-close-button" onclick="app._hideSettings();">X</div>
        <div id="userInfoContainer" class="settings-user-info-container">
          <div id="userInfoHeading" class="settings-user-info-heading">User Info</div>
          <form id="userInfoForm" class="settings-user-info-form" onsubmit="app.setUserInfo(this); return false;">
            <div id="pkField">${info.pk}</div>
            <input id="aliasField" type="text" placeholder="Alias" value="${info.alias}"/>
            <input id="descField" type="text" placeholder="Description" value="${info.desc}"/>
            <input id="imgField" type="text" placeholder="Image" value="${info.img}"/>
            <!--<div class="chat-button" onclick="app.openImageOptions();">&#x058D</div>-->
            <input type="submit" value="Save Info">
          </form>
          <a href="https://onlinepngtools.com/convert-png-to-base64" target="_blank">Generate String for Image form PNG here</a>
        </div>
        <div id="settingsSettingsContainer" class="settings-settings-container">
          <div id="settingsHeading" class="settings-settings-heading">Settings</div>
          <form id="settingsForm" class="settings-settings-form" onsubmit="app.setUserSettings(this); return false;">
            <input id="blacklistField" type="text" placeholder="Blacklist" value="${settings.blacklist}"/>
            <input type="submit" value="Save Settings">
          </form>
        </div>
        <div id="userPeerbookContainer" class="settings-peerbook-container">
        <div id="userPeerbookHeading" class="settings-user-peerbook-heading">Peerbook</div>
        </div>
      </div>`;
    }

    _hideSettings(){
      this.user.inSettings = false;
      document.getElementById('form').innerHTML = 
      ``;
    }

    _showAddLocal(){

      this.user.inAddLocal = true;

      //unselect chats in sidebar
      document.querySelectorAll('.destination').forEach(item => 
        {    
          item.classList.remove('active');
        });
      
      document.getElementById('form').innerHTML = 
      ``;
      //empty messages
      document.getElementById('messages').innerHTML = '';
      //empty header
      document.getElementById('chatHeaderInformation').innerHTML = ''
      //hide send message bar and button
      document.getElementById('chatButtonsContainer').classList.add('hidden');
      document.getElementById('msgForm').classList.add('hidden');

      document.getElementById('form').innerHTML +=
      `<div id="settingsContainer" class="settings-container" style="z-index:100; position: fixed;">
      <div class="settings-close-button" onclick="app._hideInJoinRemote();">X</div>
      <div>To add a local Server or Group you have to insert the right Public Keys</div>
      <li>For new server you can leave server pk empty but have to give an info</li>
      <li>For a new group you have to give the PK of the server where you want to add the room to and an info</li>
      <div>ServerPK</div>
         <form class="chat-form" onsubmit="app.addRoute(this); return false;">
         <input id="serverPkToAdd" type="text" placeholder="Enter Server PK" />
         <div>Info</div>
         <input id="alias" type="text" placeholder="Alias" />
         <input id="descr" type="text" placeholder="Description" />
         <input type="submit" value="Add new Server/Room">
       </form>
       </div>`;

    }

    _hideInAddLocal(){
      this.user.inAddLocal = false;
      document.getElementById('form').innerHTML = 
      ``;
    }

    _showAddRemote(){

      this.user.inJoinRemote = true;

      //unselect chats in sidebar
      document.querySelectorAll('.destination').forEach(item => 
        {    
          item.classList.remove('active');
        });
      
      document.getElementById('form').innerHTML = 
      ``;
      //empty messages
      document.getElementById('messages').innerHTML = '';
      //empty header
      document.getElementById('chatHeaderInformation').innerHTML = ''
      //hide send message bar and button
      document.getElementById('chatButtonsContainer').classList.add('hidden');
      document.getElementById('msgForm').classList.add('hidden');

      document.getElementById('form').innerHTML += 
      `<div id="settingsContainer" class="settings-container" style="z-index:100; position: fixed;">
       <div class="settings-close-button" onclick="app._hideInJoinRemote();">X</div>
       <div>To join a remote P2P or Group you have to insert the right Public Keys</div>
       <li>P2P: VisorPK & ServerPK & RoomPK = RemotePK</li>
       <li>Group/Server: VisorPK & ServerPK & RoomPK</li>
          <form class="chat-form" onsubmit="app.joinRemoteRoute(this); return false;">
          <input id="visorPkToAdd" type="text" placeholder="Enter Visor PK" />
          <input id="serverPkToAdd" type="text" placeholder="Enter Server PK" />
          <input id="roomPkToAdd" type="text" placeholder="Enter Room PK" />
          <input type="submit" value="+">
        </form>
        <div>VisorPK --------------------------- ServerPK -------------------------- RoomPK</div>
        </div>
        </div>`;
    }

    _hideInJoinRemote(){
      this.user.inJoinRemote = false;
      document.getElementById('form').innerHTML = 
      ``;
    }


    _showMessage(msg,route) {
      let c = this.user;

      switch(msg.type) {
        case 1:
          let connMsgOrigin
          if (msg.origin == c.info.pk){
            connMsgOrigin = "user"
          } else {
            connMsgOrigin = "peer"
          }
          let connMsg
          if (msg.subtype == 1){
            connMsg = "chat request from " + connMsgOrigin
          }
          else if (msg.subtype == 2){
            connMsg = "chat accepted"
          }
          else if (msg.subtype == 3){
            connMsg = "chat rejected"
          }
          else if (msg.subtype == 4){
            connMsg = "remote deleted chat"
          }
          else {
            connMsg = "undefined chat type message"
          }
          document.getElementById('messages').innerHTML += `<li class="content-center"><div class="date-container">${connMsg}</div></li>`;
        break;
      case 2:
      if (!msg.date) {
          const liClassName = msg.origin === c.info.pk  ? 'content-right' : 'content-left';
          const containerClassName = msg.origin === c.info.pk ? 'msg-sent' : 'msg-received';

          let msgArea = document.getElementById('messages');
          let mustScroll = (msgArea.scrollHeight - msgArea.scrollTop) === msgArea.userHeight;

          if (route.visor == route.server){
          //P2P-Chat
          document.getElementById('messages').innerHTML +=
            `<li class="${liClassName}"><div class="msg-container ${containerClassName}"><div>${msg.message}</div><div class="message-time">${msg.ts}</div></div></li>`;
          }
          else{
            document.getElementById('messages').innerHTML +=
            `<li class="${liClassName}"><div class="msg-container ${containerClassName}"><div class="msg-alias">${this.getAliasFromPKAndRoute(msg.origin,route)}</div><div class="message-pk">${msg.origin}</div><div>${msg.message}</div><div class="message-time">${msg.ts}</div></div></li>`;
          }
          if (mustScroll) {
            msgArea.scrollTop = msgArea.scrollHeight;
          }
        } else {
          let date = msg.date.getFullYear().toString().padStart(2, '0') + '-';
          date += (msg.date.getMonth() + 1).toString().padStart(2, '0') + '-';
          date += msg.date.getDate().toString().padStart(2, '0');

          document.getElementById('messages').innerHTML += `<li class="content-center"><div class="date-container">${date}</div></li>`;
        }
        break;
        case 3:
          let infoMsg
          if (msg.origin == c.info.pk) {
            infoMsg = "sent info to peer"
          } else {
            infoMsg = "peer updated info"
          }
         document.getElementById('messages').innerHTML += `<li class="content-center"><div class="date-container">${infoMsg}</div></li>`;
          break;
      }
    }

    _selectChat(pk) {
      if (this.chat === pk) {
        return;
      }
      let chat = this.chats[this.getChatIndexFromPK(pk)]
      let route = chat.route

      this._hideSettings();
      this._hideInAddLocal();
      this._hideInJoinRemote();

      this.chat = pk;
      document.querySelectorAll('.destination').forEach(item => {
        const pkArea = item.getElementsByClassName('pk')[0];

        if (pkArea.innerText === pk) {
          item.classList.add('active');
        } else {
          item.classList.remove('active');
        }
      });

      document.getElementById('messages').innerHTML = '';
      document.getElementById('chatHeaderInformation').innerHTML = ''
      document.getElementById('chatHeaderInformation').innerHTML += `<div>${chat.info.alias}</div>`;
      document.getElementById('chatHeaderInformation').innerHTML += `<div>${chat.info.desc}</div>`;
      document.getElementById('chatHeaderInformation').innerHTML += `<div>Visor: ${chat.route.visor}</div>`;
      document.getElementById('chatHeaderInformation').innerHTML += `<div>Server: ${chat.route.server}</div>`;
      document.getElementById('chatHeaderInformation').innerHTML += `<div>Room: ${chat.route.room}</div>`;

      this.getMessagesFromRoute(route).forEach(msg => this._showMessage(msg,route));
      document.getElementById('chatButtonsContainer').classList.remove('hidden');
      document.getElementById('msgForm').classList.remove('hidden');
      document.getElementById('msgField').focus();

      let msgArea = document.getElementById('messages');
      msgArea.scrollTop = msgArea.scrollHeight;

    }

    /////////////////////////////////////////////////////////////
  //// HTTP /user
  /////////////////////////////////////////////////////////////
  //// GET
  //returns [Info] from [User]
  async getUserInfo(){
    return fetch('user/getInfo', { method: 'GET', body: null })
      .then(async res => {
        if (res.ok) {
          return res.json().then(i => {
            var info = new Info(i.Pk,i.Alias,i.Desc,i.Img);
            return info
            });
        } else {
          res.text().then(text => alert(`Failed to get info`));
        }
      });
  }
  //returns [Settings] from [User]
  async getUserSettings(){

  return fetch('user/getSettings', { method: 'GET', body: null })
    .then(async res => {
      if (res.ok) {
        return res.json().then(s => {
          var settings = new Settings(s.Blacklist);
          return settings;
          });
      } else {
        res.text().then(text => alert(`Failed to get settings`));
      }
    });

  }

  //// PUT
  setUserInfo(el){
  let info = new Info(this.user.info.pk, el[0].value.trim(), el[1].value.trim(), el[2].value.trim());

  if (info.alias.length == 0) {
    return;
  }

  fetch('user/setInfo', { method: 'PUT', body: JSON.stringify({ alias: info.alias, desc: info.desc, img: info.img}) })
    .then(res => {
      if (res.ok) {
        res.text()
        this.getUserInfo().then(info => {
          this._updateUser(new User(info, this.user.settings));
        });
      } else {
        res.text().then(text => alert(`Failed to set info`));
      }
    });

  }

  setUserSettings(el){
  let settings = new Settings(el[0].value.trim());

  //[]:check for regex of blacklist array. --> maybe add one pk after another to blacklist.
  
  fetch('user/setSettings', { method: 'PUT', body: JSON.stringify({ blacklist: settings.blacklist}) })
    .then(res => {
      if (res.ok) {
        res.text()
        this.getUserSettings().then(settings => {
          this._updateUser(new User(this.user.info, settings));
        });
      } else {
        res.text().then(text => alert(`Failed to set settings`));
      }
    });

  }
/////////////////////////////////////////////////////////////
//// HTTP /chats
/////////////////////////////////////////////////////////////
//// GET
      getRouteHTTP(r){
        return new Route(r.Visor,r.Server,r.Room)
      }
      getInfoHTTP(i){
        return new Info(i.Pk,i.Alias,i.Desc,i.Img)
      }

      getMembersHTTP(mb){
        var members = []
        Object.keys(mb).forEach(m => { 
          var mInfo = this.getInfoHTTP(mb[m].Info)
          members.push(new Peer(mInfo,mb[m].Alias))})
        return members
      }

      getMessagesHTTP(ms){
        var msgs = []
        ms.forEach(m => { msgs.push(new Message(m.Id,m.Origin,m.Time,m.Root,m.Dest,m.Msgtype,m.MsgSubtype,m.Message,m.Status,m.Seen))})
        return msgs
      }

      getRoomHTTP(r){
        let info = this.getInfoHTTP(r.Info)
        var msgs = []
        if (r.Msgs != null){
          msgs = this.getMessagesHTTP(r.Msgs)
        }
        let members = []
        if (r.Members != null){
          if (Object.keys(r.Members).length){
            members = this.getMembersHTTP(r.Members)
          }
        }
        let route = this.getRouteHTTP(r.PKRoute)
        let room = new Room(route,info,msgs,r.IsVisible,r.Type,members,r.Mods,r.Muted,r.Blacklist,r.Whitelist)
        console.log("getRoomHTTP")
        console.log(room)
        return room
      }

      getRoomsHTTP(rms){
        var rooms = []
        Object.keys(rms).forEach(r => rooms.push(this.getRoomHTTP(rms[r])))
        return rooms
      }

      getServerHTTP(s){
        var info = new Info(s.Info.Pk,s.Info.Alias,s.Info.Desc,s.Info.Img)
        var members = []
        if (s.Members != null){
          if (Object.keys(s.Members).length){
            members = this.getMembersHTTP(s.Members)
          }
        }
        var rooms = this.getRoomsHTTP(s.Rooms)
        var route = this.getRouteHTTP(s.PKRoute)
        var server = new Server(route,info,members,s.Admins,s.Muted,s.Blacklist,s.whitelist,rooms)
        return server
      }

      //returns an array of [Chat]
      async getChatAll(){

        return fetch('chats', { method: 'GET', body: null })
          .then(res => {
            if (res.ok) {
              return res.json().then(visors => {

                var v_ = []
                if (visors != null){
                  visors.forEach( v => {
                    console.log(v)
                    var p2p
                    if (v.P2P != null && v.P2P.Type != 0) {
                      if (Object.keys(v.P2P).length){
                        p2p = this.getRoomHTTP(v.P2P)
                      }
                    } else {
                      p2p = null
                    }
                    var s_ = []
                    if (v.Server != null){
                      Object.keys(v.Server).forEach( s => {
                        s_.push(this.getServerHTTP(v.Server[s]))
                      })
                    }
                    v_.push(new Visor(v.Pk,p2p,s_))
                  })

                  return this.getSortedChats(v_)
                }
                else {
                  console.log("No chats available")
                }
              });
            } else {
              res.text().then(text => alert(`Failed to get chats`));
            }
          });
      }

      //returns all chats so UI can display it
      getSortedChats(visors){
        //for the first working skychat there is no sorting
        var cs = []
        Object.keys(visors).forEach(v => {
          if (visors[v].p2p != null){
            cs.push(visors[v].p2p)
          }
          Object.keys(visors[v].server).forEach(s => {
            Object.keys(visors[v].server[s].rooms).forEach(r =>{
              cs.push(visors[v].server[s].rooms[r])
            })
          })
        })
        return cs
      }

      //returns the [Room] with the given route
      async getRoomByRoute(route){
        const visorpk = route.visor
        const serverpk = route.server
        const roompk = route.room

        const params = new URLSearchParams({visor: visorpk, server: serverpk,room: roompk})

        return fetch('chats/'+ 'getRoom?' + params, { method: 'GET', body: null})
          .then( async res => {
            if (res.ok) {
              return res.json().then(r => {
                console.log(r)
                return this.getRoomHTTP(r)
              })
            } else {
              res.text().then(text => alert(`Failed to get chat:  ${text}`));
            }
          }).catch(e => alert(e.message));
          ;

      }
//// POST
      //addRoute adds a new Server or Room in dependency on the given info
      addRoute(el){
        const visorpk = this.user.info.pk
        const serverpk = this.processPk(el[0].value.trim());
        const alias = el[1].value.trim();
        let description = el[2].value.trim();

          if (alias == ""){
            alert('Please enter an alias')
            return;
          }

          if (description == ""){
            alert('Description was not given and therefore set to "-"')
            description = '-'
          } 

        if (serverpk != ""){
          if (serverpk.length != 66) {
            alert('ServerPK: Public keys must be 66 characters long.')
            return;
          }

          if (!/^[0-9a-fA-F]+$/.test(serverpk)) {
            alert('ServerPK: The public key includes invalid characters.')
            return;
          }

          fetch('chats/' + "sendAddRoomMessage", { method: 'POST', body: JSON.stringify({ visorpk: visorpk, serverpk: serverpk, alias: alias, desc: description, img: null, type:null}) })
          .then(res => {
            if (res.ok) {
              res.text()
            } else {
              res.text().then(text => alert(`Failed to add room: ${text}`));
            }
          }).catch(e => alert(e.message));   
        }else{
          fetch('chats/' + "addLocalServer", { method: 'POST', body: JSON.stringify({alias: alias, desc: description, img: ""}) })
          .then(res => {
            if (res.ok) {
              res.text()
            } else {
              res.text().then(text => alert(`Failed to add local server: ${text}`));
            }
          }).catch(e => alert(e.message));       
          }
        }

      //tries to add the given route
      //returns nothing
      joinRemoteRoute(el) {
        const visorpk = this.processPk(el[0].value.trim());
        const serverpk = this.processPk(el[1].value.trim());
        const roompk = this.processPk(el[2].value.trim());


        //for the moment it is only possible to add perfectly fine defined routes.
        //TODO: Make this more user friendly

        if (visorpk.length != 66) {
          alert('VisorPK: Public keys must be 66 characters long.')
          return;
        }

        if (!/^[0-9a-fA-F]+$/.test(visorpk)) {
          alert('VisorPK: The public key includes invalid characters.')
          return;
        }

        if (serverpk != "") {
          if (serverpk.length != 66) {
            alert('ServerPK: Public keys must be 66 characters long.')
            return;
          }
  
          if (!/^[0-9a-fA-F]+$/.test(serverpk)) {
            alert('ServerPK: The public key includes invalid characters.')
            return;
          }
        }

        if (roompk != ""){

          if (roompk.length != 66) {
            alert('RoomPK: Public keys must be 66 characters long.')
            return;
          }

          if (!/^[0-9a-fA-F]+$/.test(roompk)) {
            alert('RoomPK: The public key includes invalid characters.')
            return;
          }
        }

        if (visorpk == this.user.info.pk){
          alert('You do not have to join a server that is hosted on your visor')
          return;
        }

        document.getElementById('visorPkToAdd').value = "";
        document.getElementById('serverPkToAdd').value = "";
        document.getElementById('roomPkToAdd').value = "";


        //TODO: Make if or switch case -> when visorpk is same as userpk and serverpk and roompk is empty then make new server with one room
        //and if visorpk and serverpk is defined but roompk is empty then add new room to server
        fetch('chats/' + "joinRemoteRoute", { method: 'POST', body: JSON.stringify({ visorpk: visorpk, serverpk: serverpk, roompk: roompk}) })
          .then(res => {
            if (res.ok) {
              res.text()
            } else {
              res.text().then(text => alert(`Failed to add chat: ${text}`));
            }
          })
          .catch(e => alert(e.message));
      }

      //tries to send a [Message] to the current selected [Chat]
      //returns nothing
      sendMessage(el) {
        const msg = el[0].value;

        if (msg.length == 0) {
          return;
        }

        let route = this.chats[this.getChatIndexFromPK(this.chat)].route

        const visorpk = route.visor;
        const serverpk = route.server;
        const roompk = route.room;

        fetch('chats/' + "sendTxtMsg", { method: 'POST', body: JSON.stringify({ visorpk: visorpk, serverpk: serverpk, roompk: roompk, message: msg}) })
        .then(res => {
            if (res.ok) {
              res.text()
              el[0].value = '';
            } else {
              res.text().then(text => alert(`Failed to send message: ${text}`));
            }
          })
        .catch(e => alert(e.message));
      }
//// DELETE

      //tries to leave the given route
      leaveRemoteRoute() {  
        if (!this.chat) {
          return;
        }

        let route = null
        Object.keys(this.chats).forEach(c => {
          if (this.chats[c].pk == this.chat){
            route = this.chats[c].route;
          }
        })

        if (route == null){
          return;
        }

        const response = window.confirm("Are you sure you want to leave the chat?");

        const visorpk = route.visor
        const serverpk = route.server
        const roompk = route.room

        if (response) {
          fetch('chats' + '/leaveRemoteRoute', { method: 'POST', body: JSON.stringify({ visorpk: visorpk, serverpk: serverpk, roompk: roompk}) })
          .then(res => {
            if (res.ok) {
              res.text().then();
              this.chats = this.chats.filter(v => v.pk != roompk);
              
              document.getElementById('messages').innerHTML = '';
              document.getElementById('chatButtonsContainer').classList.add('hidden');
              document.getElementById('msgForm').classList.add('hidden');
              document.querySelectorAll('.destination').forEach(item => {
              
              const pkArea = item.getElementsByClassName('pk')[0];

              if (pkArea.innerText === roompk) {
                item.parentNode.removeChild(item);
              }});
              this.chat = null;
            } else {
              res.text().then(text => alert(`Failed to leave chat:\n visor:\n${visorpk}\nserver:\n${serverpk}\nroom:\n${roompk}\nreason:\n${text}`));
            }
          })

        }
        else{
          return;
        }
      }

      //tries to delete the given route
      deleteRoute() {  
        if (!this.chat) {
          return;
        }

        let route = null
        Object.keys(this.chats).forEach(c => {
          if (this.chats[c].pk == this.chat){
            route = this.chats[c].route;
          }
        })

        if (route == null){
          return;
        }

        const response = window.confirm("Are you sure you want to delete the chat?");

        const visorpk = route.visor
        const serverpk = route.server
        const roompk = route.room

        if (response) {
          fetch('chats' + '/deleteRoute', { method: 'POST', body: JSON.stringify({ visorpk: visorpk, serverpk: serverpk, roompk: roompk}) })
          .then(res => {
            if (res.ok) {
              res.text().then();
              this.chats = this.chats.filter(v => v.pk != roompk);
              
              document.getElementById('messages').innerHTML = '';
              document.getElementById('chatButtonsContainer').classList.add('hidden');
              document.getElementById('msgForm').classList.add('hidden');
              document.querySelectorAll('.destination').forEach(item => {
              
              const pkArea = item.getElementsByClassName('pk')[0];

              if (pkArea.innerText === roompk) {
                item.parentNode.removeChild(item);
              }});
              this.chat = null;
            } else {
              res.text().then(text => alert(`Failed to delete chat:\n visor:\n${visorpk}\nserver:\n${serverpk}\nroom:\n${roompk}\nreason:\n${text}`));
            }
          })

        }
        else{
          return;
        }
      }

/////////////////////////////////////////////////////////////
//// HTTP /notification
/////////////////////////////////////////////////////////////
//// Subscribe


      async getWebsocketPort() {
          return fetch('notifications/websocket', { method: 'GET', body: null })
            .then(async res => {
              if (res.ok) {
               return res.text().then(text => {
                  return text;
                });
              } else {
                res.text().then(text => alert(`Failed to get websocket`));
              }
            });
      }

      notificationsSubscribe(port) {
        var socket = new WebSocket('ws://localhost'+ port + '/notifications');
        
        socket.onmessage = async(event) => {
          const data = JSON.parse(event.data);
          console.log(data)
          console.log("Notification DataType: " + data.type)
          const notifMessage = JSON.parse(data.message)
          let visorpk = notifMessage.visorpk
          let serverpk = notifMessage.serverpk
          let roompk = notifMessage.roompk
          let route = new Route(visorpk,serverpk,roompk)


            switch(data.type) {
              //NewAddRouteNotifyType
              case 1:
                console.log("new add route notification")
                await this.getRoomByRoute(route).then(c => {
                  if (this.chats == null){
                    this.chats = []
                  }
                  this._addChat(c)
                  this._selectChat(roompk)
                })
                break;
              //NewChatNotifType
              case 2:
                console.log("new peer chat notification")
                await this.getRoomByRoute(route).then(chat => {
                  if (this.chats == null){
                    this.chats = []
                  }
                  this._addChat(chat)
                })
                break;
              //NewMsgNotifType
              case 3:
                console.log("new message notification") 
                 //Fetch data of chat with new message from HTTP
                await this.getRoomByRoute(route).then(c => {
                  if (this.chats == null){
                    this.chats = []
                    this._addChat(c)
                  }
                  this._updateChat(c)
                })
                break;
              default:
                console.log("unknown notification")
                break;
            }            
          }
        socket.onerror = async(event) => {
          console.error("EventSource failed:", event)
        }
        socket.onclose = async() => {
          console.log("socket opened")
        }
        socket.onclose = async() => {
          console.log("socket closed")
        }
      };

/////////////////////////////////////////////////////////////
//// Helper Functions to get or process sub steps
/////////////////////////////////////////////////////////////
    //returns the given pk as lowerCase representation
    processPk(pk) {
      return pk.toLowerCase();
    }

    //returns the index inside this.chats of the given route
    getChatIndexFromPK(pk) {
      let arr = this.chats;
      for (var i=0, iLen=arr.length; i<iLen;i++){
        if (arr[i].pk == pk) return i;
      }
    }

    //returns the index inside this.chats of the given route
    getChatIndexFromRoute(route) {
      let arr = this.chats;
      for (var i=0, iLen=arr.length; i<iLen;i++){
        if (arr[i].route.room == route.room) return i;
      }
    }

    //returns an array of [Message] of the given route
    getMessagesFromRoute(route) {
       return this.chats[this.getChatIndexFromRoute(route)].messages;
    }

    //TODO: return fixed strings depending on type of last message (e.g. InfoType or so)
    //returns the last [Message] of the given pk/chat
    getLastMessageFromRoute(route){
      let arr = this.chats;
      for (var i=0, iLen=arr.length; i<iLen;i++){
          if (arr[i].messages.length != 0){
            if (arr[i].route.room === route.room) return arr[i].messages[arr[i].messages.length - 1].message;
          }
      }
      return "New Chat" 
    }

    //returns alias of given member of route
    getAliasFromPKAndRoute(pk,route){

      //first try to get alias from peerbook
      if (this.user.peerbook != undefined){
        let peers = this.user.peerbook.peers;
        for (var i=0, iLen=peers.length; i<iLen;i++){
          if (peers[i].info.pk == pk) return peers[i].alias;
        }
      }

      //then try to get alias from group member list
      let chat = this.chats[this.getChatIndexFromRoute(route)]
      if (chat.members != undefined){
        let arr = chat.members;
        for (var i=0, iLen=arr.length; i<iLen;i++){
          if (arr[i].info.pk == pk) return arr[i].info.alias;
        }
      }
      return "No Member"
    }

  }

  function createSkychat(){
    let x = new Skychat()
    let test = false
    return x.fetchData(test)
  }

  createSkychat().then(skychat => {
    window.app = skychat
  });

  function showDropdown() {
    document.getElementById("myDropdown").classList.toggle("show");
  }
  
  // Close the dropdown if the user clicks outside of it
  window.onclick = function(event) {
    if (!event.target.matches(".dropbtn")) {
      var dropdowns = document.getElementsByClassName("dropdown-content");
      var i;
      for (i = 0; i < dropdowns.length; i++) {
        var openDropdown = dropdowns[i];
        if (openDropdown.classList.contains("show")) {
          openDropdown.classList.remove("show");
        }
      }
    }
  }