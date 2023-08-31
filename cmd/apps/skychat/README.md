# Skywire Chat App

Chat implements basic text messaging between skywire visors.

It is possible to send messages p2p or in groups.

The group feature is structured in a way that it is possible for everyone to host and join multiple servers.

Those servers are structured like that:

  - Public Key of Visor
    - Public Key of Server 1
      - Public Key of Room 1.1
      - Public Key of Room 1.2
      - ...
    - Public Key of Server 2
      - Public Key of Room 2.1
      - Public Key of Room 2.2

And the chats are adressed with a so called public key route (pkroute):
  - Route to a Room = [PK of Visor, PK of Server, PK of Room]
  - P2P Route       = [PK of Visor, PK of Visor, PK of Visor]


Messaging UI is exposed via web interface.

Chat only supports one WEB client user at a time.


# Development Info
The app is written with 'Clean Architecture' based on the blog entry of Panayiotis Kritiotis [Clean Architecture in Go](https://pkritiotis.io/clean-architecture-in-golang/)

To get a basic understanding on how it is structured, reading the blog will help.

## Sequence Diagrams
### Init
```plantuml
@startuml
Main -> Main: flag.Parse()
Main -> Main: init Services
Main -> MessengerService.Listen: go
activate MessengerService.Listen
deactivate MessengerService.Listen

Main -> HTTPServer: ListenAndServe
activate HTTPServer
deactivate
@enduml
```

### MessengerService.Listen
```plantuml
@startuml
box "MessengerService"
participant Listen
participant Handle
participant ErrorRoutine
end box

Listen -> CliRepo: GetClient
CliRepo --> Listen: Client
Listen -> ErrorRoutine: go(error channel)
activate ErrorRoutine
deactivate
Listen -> Listen: for
activate Listen
Listen -> Listen: l.Accept()
Listen -> Listen: Client: AddConnection
Listen -> CliRepo: SetClient
CliRepo --> Listen: errSetClient
Listen -> Handle: go(cipher.PubKey)
activate Handle
deactivate
@enduml
```

### MessengerService.Handle
```plantuml
@startuml
box "MessengerService"
participant Handle
participant handleP2PMessage
participant handleRemoteServerMessage
participant handleLocalServerMessage
end box

-> Handle: cipher.PubKey
Handle -> CliRepo: GetClient
CliRepo --> Handle: Client
Handle -> Handle: conn := Client.GetConn(cipher.PubKey)
Handle -> Handle: for
activate Handle
Handle -> Handle: err := conn.Read(buf)
alt #Pink err != nil
    Handle -> Handle: Client.DeleteConn(cipher.PubKey)
else #LightBlue err = nil
    Handle ->  Handle: json.Unmarshal(buf)
    alt P2P
      Handle -> handleP2PMessage: go (Message)
      activate handleP2PMessage
      deactivate handleP2PMessage
    else RemoteServerMessage
      Handle -> handleRemoteServerMessage: go (Message)
      activate handleRemoteServerMessage
      deactivate handleRemoteServerMessage
    else LocalServerMessage
      Handle -> handleLocalServerMessage: go (Message)
      activate handleLocalServerMessage
      deactivate handleLocalServerMessage
    end
end
deactivate
@enduml
```
### MessengerService.handleP2PMessage
```plantuml
@startuml
participant Alice.Notification order 9
participant Alice.VisorRepo order 10
participant Alice order 20
participant Alice2.VisorRepo order 25
participant Alice2 order 25
participant Bob order 30
participant Bob.VisorRepo order 40
participant Bob.Notification order 50
== Handling ChatRequest ==
Alice -> Bob: ChatRequestMessage
activate Bob
alt NotInBlacklist
  Bob <-> Bob.VisorRepo: visor = GetByPK
  Bob -> Bob: visor.AddMessage
  Bob -> Bob.VisorRepo: SetVisor
  Bob -> Bob.Notification: NewP2PChatNotification & Notify
  Bob -> Alice: ChatAcceptMessage
  activate Alice
  Bob -> Alice2: InfoMessage
  deactivate Bob
  activate Alice2
  Alice2 -> Alice2: visor.SetRouteInfo
  Alice2 -> Alice2.VisorRepo: SetVisor
  Alice2 -> Alice.Notification: NewMsgNotification & Notify
  deactivate Alice2
  Alice <-> Alice.VisorRepo: visor = GetByPK
  Alice -> Alice: visor.AddMessage
  Alice -> Alice.VisorRepo: SetVisor
  Alice -> Alice.Notification: NewMsgNotification & Notify
  Alice -> Bob: InfoMessage
  deactivate Alice
  activate Bob
  Bob -> Bob: visor.SetRouteInfo
  Bob -> Bob.VisorRepo: SetVisor
  Bob -> Bob.Notification: NewMsgNotification & Notify
  deactivate Bob
  deactivate Alice2
else InBlacklist
  Bob -> Alice: ChatRejectMessage
  activate Bob
  activate Alice
  Bob -> Bob.VisorRepo: GetByPK
  Bob.VisorRepo --> Bob: Visor
  Bob -> Bob.VisorRepo: if no server of visor DeleteVisor
  deactivate Bob
  Alice <-> Alice.VisorRepo: visor = GetByPK
  Alice -> Alice: visor.AddMessage
  Alice -> Alice.VisorRepo: SetVisor
  Alice -> Alice.Notification: NewMsgNotification & Notify
  deactivate Alice
end

== Handling Text Messages ==
Alice -> Bob: TextMessage
activate Bob
Bob -> Bob: visor.AddMessage
Bob -> Bob.VisorRepo: SetVisor
Bob -> Bob.Notification: NewMsgNotification & Notify
deactivate Bob

== Handling Info Messages ==
Alice -> Bob: InfoMessage
activate Bob
Bob -> Bob: visor.AddMessage
Bob -> Bob.VisorRepo: SetVisor
Bob -> Bob: json.Unmarshal -> to info.Info
Bob -> Bob: visor.SetRouteInfo(info)
Bob -> Bob.VisorRepo: SetVisor
Bob -> Bob.Notification: NewMsgNotification & Notify
deactivate Bob

@enduml
```

### MessengerService.handleRemoteServerMessage (This is the client-side handling of servers)
TODO
### MessengerService.handleLocalServerMessage (This is the server-side handling of servers)
TODO

### Usecases
TODO
