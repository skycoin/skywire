# pfilter
Small Go package for filtering packets from a single net.PacketConn into multiple virtual net.PacketConn's based on some predicate.

Used to multiplex/weave in STUN packets on top of an existing UDP connection, where IP address based routing would not work due to STUN sending replies back from random addresses.

Connections also implement `PacketReceiver`. Set a receiver before starting the
filter to process claimed packets on its read goroutine instead of calling
`ReadFrom`:

```go
conn.(pfilter.PacketReceiver).SetPacketReceiver(func(data []byte, addr net.Addr, err error) {
    // Process data before returning; copy it if it must outlive this call.
    // A terminal read error is delivered with data == nil.
})
```

Callbacks must return promptly because they run on the shared read goroutine.
They may close their own connection. Passing nil restores queueing for subsequent
packets; previously queued packets remain readable, and an in-flight callback
may still finish after the receiver is replaced.


