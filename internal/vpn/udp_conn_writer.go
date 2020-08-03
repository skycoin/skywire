package vpn

import "net"

type UDPConnWriter struct {
	conn *net.UDPConn
	to   *net.UDPAddr
}

func NewUDPConnWriter(conn *net.UDPConn, to *net.UDPAddr) *UDPConnWriter {
	return &UDPConnWriter{
		conn: conn,
		to:   to,
	}
}

func (w *UDPConnWriter) Write(b []byte) (int, error) {
	return w.conn.WriteToUDP(b, w.to)
}
