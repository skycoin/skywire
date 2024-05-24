# Packets

The *Node Module* handles data encapsulated within data units called *Packets*. *Packets* can be grouped within the following categories based on their use-case;

- ***Settlement Packets*** are used by the *Transport Manager* to "settle" Transports. Settlement, allows the two nodes that are the edges of the transport to decide on the *Transport ID* to be used, and whether the Transport is to be public. Only after a *Transport* is settled, can the *Router* have access to the Transport.

    *Settlement Packets* contain `json` encoded payload.

- ***Foundational Packets*** are used by a *Router* to communicate with a remote *Setup Node* and is used for setting up, establishing and destroying routes.

    *Foundational Packets* are prefixed by 3 bytes: the packet size (2 bytes) and a Type (1 byte) that contains the foundational packet type.

- ***Data Packets*** are Packets that are actually used to encapsulate data delivered between two Apps.

    *Data Packets* are prefixed by 6 bytes; including the packet size (2 bytes) and the Route ID (4 bytes) which can have any value other than `0x00` or `0x01`.
    
- ***Loopback Packets*** are packets that are consumed locally by the node.

    *Loopback Packets* are structurally similar to data packets but their Route ID links to a rule that specifies which app to forward the packet to.

## Settlement Packets

After a Transport is established between two nodes, the nodes needs to decide on the *Transport ID* that describes the Transport and whether the Transport is to be public or private (public Transports are to be registered in the *Transport Discovery*). This process is called the *Settlement Handshake*.

The Packets of this handshake contain `json` encoded messages.

*Settlement Handshake* packets do not need a field for Packet-type are they are expected in a specific order.

- Request to settle transport is sent by the *Transport Initiator* to the *Transport Responder* after a *Transport* connection is established.

    JSON Body: Contains a `transport.SignedEntry` structure with the *Transport Initiator*'s signature.

- *Transport Responder* should validate submitted `transport.SignedEntry`, and if entry is valid it should add sign it and perform transport registration in transport discovery. If registration was successful responder should send updated `transport.SignedEntry` back to initiator. 

    JSON Body: Contains a `transport.SignedEntry` structure with signatures from both the *Transport Initiator* and the *Transport Responder*. If the transport is registered in *Transport Discovery*, the `SignedTransport.Registered` should contain the epoch time of registration.

If transport will fail at any step participants can chose to stop handshake procedures and close corresponding transport. Transport disconnect during the handshake should be handled appropriately by participants. Optional handshake timeout should also be supported.

## Foundational Packets

Foundational packets are used for the communication between *App Nodes* and *Setup Nodes*.

The *Setup Node* is responsible for fulfilling Route initiating and destroying requests by communicating with the initiating, responding and intermediate nodes of the proposed route.

The following is the expected format of a Foundational Packet;

```
| Packet Len | Type   | JSON Body |
| 2 bytes    | 1 byte | ~         |
```

- ***Packet Len*** specifies the total packet length in bytes (exclusive of the *Packet Len* field).
- ***Type*** specifies the *Foundational Packet Type*.
- ***JSON Body*** is the packet body (in JSON format) that is unique depending on the packet type.

**Foundational Packet Types Summary:**

| Type | Name |
| ---- | ---- |
| 0x00 | `AddRules` |
| 0x01 | `RemoveRules` |
| 0x02 | `CreateLoop` |
| 0x03 | `ConfirmLoop` |
| 0x04 | `CloseLoop` |
| 0x05 | `LoopClosed` |
| 0xfe | `ResponseFailure` |
| 0xff | `ResponseSuccess` |

### `0x00 AddRules`

Sent by the *Setup Node* to all *Nodes* of the route. This packet informs nodes what rules are to be added to their internal routing table.

**JSON Body:**

```json
[<rule-1>, <rule-2>]
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with
    ```json
    [<rid-1>, <rid-2>]
    ```
    
### `0x01 RemoveRules`

Sent by the *Setup Node* to *Node* of the route.

**JSON Body:**

```json
["<rid-1>", "rid-2"]
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with
    ```json
    [<rid-1>, <rid-2>]
    ```

### `0x02 CreateLoop`

Sent by the *Route Initiator* to a *Setup Node* to have a *Loop* created.

**JSON Body:**

```json
{
    "local-port": <local-port>,
    "remote-port": <remote-port>,
    "forward": [
        {
            "from": "<pk-1>",
            "to": "<pk-2>",
            "tid": "<tid-1>"
        },
        {
            "from": "<pk-2>",
            "to": "<pk-3>",
            "tid": "<tid-2>"
        }
    ],
    "reverse": [
        {
            "from": "<pk-3>",
            "to": "<pk-2>",
            "tid": "<tid-2>"
        },
        {
            "from": "<pk-2>",
            "to": "<pk-1>",
            "tid": "<tid-1>"
        }
    ],
    "expiry": "<expiry>"
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty payload.
    
### `0x3 ConfirmLoop`

Sent by the *Setup Node* to Responder and Initiator *Node* to confirm notify about route in opposite direction.

**JSON Body:**

```json
{
    "remote-pk": "<pk>",
    "remote-port": <remote-port>,
    "local-port": <local-port>,
    "resp-rid": <resp-rid>
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty payload.

### `0x4 CloseLoop`

Sent by a Responder or Initiator *Node* to a *Setup Node* to notify about closing a loop locally.

**JSON Body:**

```json
{
    "port": "<local-port>",
    "remote": {
        "port": <remote-port>,
        "pk": <remote-pk>
    }
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty payload.

### `0x5 LoopClosed`

Sent by a *Setup Node* to a Responder or Initiator to notify about closed loop on the opposite end.

**JSON Body:**

```json
{
    "port": "<local-port>",
    "remote": {
        "port": <remote-port>,
        "pk": <remote-pk>
    }
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty payload.


## Data Packets

The follow is the structure of a *Data Packet*.

```
| Packet Len | Route ID | Payload |
| 2 bytes    | 4 bytes  | ~       |
```
