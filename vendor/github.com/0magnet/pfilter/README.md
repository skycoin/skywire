# pfilter
Small Go package for filtering packets from a single net.PacketConn into multiple virtual net.PacketConn's based on some predicate.

Used to multiplex/weave in STUN packets on top of an existing UDP connection, where IP address based routing would not work due to STUN sending replies back from random addresses.


