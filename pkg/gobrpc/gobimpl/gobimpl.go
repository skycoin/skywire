// Package gobimpl is a dependency-free reimplementation of the subset of
// net/rpc the skywire router uses, over encoding/gob. It speaks the
// IDENTICAL gob wire protocol as net/rpc's default codec:
//
//	request:  gob(Request{ServiceMethod, Seq})        then gob(args)
//	response: gob(Response{ServiceMethod, Seq, Error}) then gob(reply)
//
// so a gobimpl peer interoperates with a stdlib net/rpc peer unchanged
// (verified in gobimpl_test.go, which connects gobimpl to net/rpc both
// ways over net.Pipe).
//
// It exists because net/rpc pulls net/http, which does not compile on the
// TinyGo js/wasm target; pkg/gobrpc aliases to this package under TinyGo
// and to net/rpc natively. The package itself imports only encoding/gob,
// reflect, and stdlib sync/io — all TinyGo-portable — so it compiles in
// both worlds and is unit-tested in CI on native.
package gobimpl

import (
	"encoding/gob"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
)

// ErrShutdown is returned when a call is attempted on a closing client,
// matching net/rpc.ErrShutdown's message.
var ErrShutdown = errors.New("connection is shut down")

// Request / Response mirror net/rpc.Request / net/rpc.Response by field name
// and type so gob encodes them wire-identically (gob matches struct fields by
// name and ignores the Go type name).
type Request struct {
	ServiceMethod string
	Seq           uint64
}

// Response is the gob response header. See Request.
type Response struct {
	ServiceMethod string
	Seq           uint64
	Error         string
}

// ServerError represents an error returned from the remote side, mirroring
// net/rpc.ServerError.
type ServerError string

func (e ServerError) Error() string { return string(e) }

// discard reads and throws away the next gob value of any type, keeping the
// stream aligned. It uses DecodeValue(zero) — gob's documented discard path —
// rather than Decode(nil), which `go vet` flags as a non-pointer call.
func discard(dec *gob.Decoder) error { return dec.DecodeValue(reflect.Value{}) }

// -------------------------------------------------------------------------
// Client
// -------------------------------------------------------------------------

// Call represents an active RPC, field-compatible with net/rpc.Call.
type Call struct {
	ServiceMethod string
	Args          any
	Reply         any
	Error         error
	Done          chan *Call
}

func (call *Call) done() {
	select {
	case call.Done <- call:
	default:
		// done is buffered (Go allocates cap 10 when nil); a blocked send
		// means the caller isn't reading — drop, matching net/rpc, rather
		// than wedge the input goroutine.
	}
}

// Client speaks the net/rpc gob protocol over a single connection.
type Client struct {
	conn io.ReadWriteCloser
	enc  *gob.Encoder
	dec  *gob.Decoder

	reqMu sync.Mutex // guards the request header+body write pair
	req   Request

	mu       sync.Mutex // guards the fields below
	seq      uint64
	pending  map[uint64]*Call
	closing  bool // user called Close
	shutdown bool // read error / peer hung up
}

// NewClient returns a Client handling requests to the services at the other
// end of conn, mirroring net/rpc.NewClient.
func NewClient(conn io.ReadWriteCloser) *Client {
	c := &Client{
		conn:    conn,
		enc:     gob.NewEncoder(conn),
		dec:     gob.NewDecoder(conn),
		pending: make(map[uint64]*Call),
	}
	go c.input()
	return c
}

func (c *Client) send(call *Call) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	c.mu.Lock()
	if c.closing || c.shutdown {
		c.mu.Unlock()
		call.Error = ErrShutdown
		call.done()
		return
	}
	seq := c.seq
	c.seq++
	c.pending[seq] = call
	c.mu.Unlock()

	c.req.Seq = seq
	c.req.ServiceMethod = call.ServiceMethod
	var err error
	if err = c.enc.Encode(&c.req); err == nil {
		err = c.enc.Encode(call.Args)
	}
	if err != nil {
		c.mu.Lock()
		call = c.pending[seq]
		delete(c.pending, seq)
		c.mu.Unlock()
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

func (c *Client) input() {
	var err error
	for err == nil {
		var resp Response
		if err = c.dec.Decode(&resp); err != nil {
			break
		}
		seq := resp.Seq
		c.mu.Lock()
		call := c.pending[seq]
		delete(c.pending, seq)
		c.mu.Unlock()

		switch {
		case call == nil:
			// Seq abandoned (Close raced the response); discard the body.
			err = discard(c.dec)
		case resp.Error != "":
			call.Error = ServerError(resp.Error)
			// The server still writes a body (invalidReply) after an error
			// response; discard it so the stream stays aligned.
			err = discard(c.dec)
			call.done()
		default:
			if derr := c.dec.Decode(call.Reply); derr != nil {
				call.Error = errors.New("reading body " + derr.Error())
			}
			call.done()
		}
	}
	// Terminate any pending calls.
	c.mu.Lock()
	c.shutdown = true
	if err == io.EOF {
		if c.closing {
			err = ErrShutdown
		} else {
			err = io.ErrUnexpectedEOF
		}
	}
	for _, call := range c.pending {
		call.Error = err
		call.done()
	}
	c.pending = make(map[uint64]*Call)
	c.mu.Unlock()
}

// Go invokes the function asynchronously, mirroring net/rpc.Client.Go. A nil
// done is allocated a buffered channel; a supplied done must be buffered.
func (c *Client) Go(serviceMethod string, args, reply any, done chan *Call) *Call {
	call := &Call{ServiceMethod: serviceMethod, Args: args, Reply: reply}
	if done == nil {
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		panic("gobrpc: done channel is unbuffered")
	}
	call.Done = done
	c.send(call)
	return call
}

// Call invokes the named function, waits for it to complete, and returns its
// error status, mirroring net/rpc.Client.Call.
func (c *Client) Call(serviceMethod string, args, reply any) error {
	call := <-c.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
}

// Close closes the underlying connection, mirroring net/rpc.Client.Close.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return ErrShutdown
	}
	c.closing = true
	c.mu.Unlock()
	return c.conn.Close()
}

// -------------------------------------------------------------------------
// Server
// -------------------------------------------------------------------------

var typeOfError = reflect.TypeOf((*error)(nil)).Elem()

type methodType struct {
	method    reflect.Method
	argType   reflect.Type
	replyType reflect.Type
}

type service struct {
	name   string
	rcvr   reflect.Value
	typ    reflect.Type
	method map[string]*methodType
}

// Server speaks the net/rpc gob protocol on the serving side.
type Server struct {
	mu         sync.Mutex
	serviceMap map[string]*service
}

// NewServer returns a new Server, mirroring net/rpc.NewServer.
func NewServer() *Server { return &Server{serviceMap: make(map[string]*service)} }

// Dial connects to a gobrpc server at the given network address, mirroring
// net/rpc.Dial.
func Dial(network, address string) (*Client, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

// Accept accepts connections on the listener and serves requests for each
// incoming connection, mirroring net/rpc.Server.Accept. It blocks; callers
// typically invoke it in a goroutine.
func (server *Server) Accept(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		go server.ServeConn(conn)
	}
}

func isExportedOrBuiltin(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.PkgPath() == "" { // builtin
		return true
	}
	name := t.Name()
	return name != "" && strings.ToUpper(name[:1]) == name[:1]
}

// Register publishes the receiver's methods that follow the net/rpc method
// signature `func(args T1, reply *T2) error`, mirroring net/rpc.Server.Register.
func (server *Server) Register(rcvr any) error {
	return server.RegisterName(reflect.Indirect(reflect.ValueOf(rcvr)).Type().Name(), rcvr)
}

// RegisterName is like Register but uses the provided name for the service
// rather than the receiver's concrete type name, mirroring
// net/rpc.Server.RegisterName.
func (server *Server) RegisterName(name string, rcvr any) error {
	s := &service{
		typ:    reflect.TypeOf(rcvr),
		rcvr:   reflect.ValueOf(rcvr),
		method: make(map[string]*methodType),
		name:   name,
	}
	if s.name == "" {
		return errors.New("gobrpc.Register: no service name for type " + s.typ.String())
	}

	for m := 0; m < s.typ.NumMethod(); m++ {
		method := s.typ.Method(m)
		mtype := method.Type
		if method.PkgPath != "" { // unexported
			continue
		}
		if mtype.NumIn() != 3 || mtype.NumOut() != 1 { // receiver, args, *reply -> error
			continue
		}
		argType, replyType := mtype.In(1), mtype.In(2)
		if !isExportedOrBuiltin(argType) || !isExportedOrBuiltin(replyType) {
			continue
		}
		if replyType.Kind() != reflect.Ptr {
			continue
		}
		if mtype.Out(0) != typeOfError {
			continue
		}
		s.method[method.Name] = &methodType{method: method, argType: argType, replyType: replyType}
	}

	if len(s.method) == 0 {
		return errors.New("gobrpc.Register: type " + s.name + " has no exported methods of suitable type")
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.serviceMap == nil {
		server.serviceMap = make(map[string]*service)
	}
	if _, dup := server.serviceMap[s.name]; dup {
		return errors.New("gobrpc.Register: service already defined: " + s.name)
	}
	server.serviceMap[s.name] = s
	return nil
}

// ServeConn runs the server on a single connection, blocking until the client
// hangs up, mirroring net/rpc.Server.ServeConn with the gob codec. Each request
// is dispatched in its own goroutine; responses are serialized by a send mutex.
func (server *Server) ServeConn(conn io.ReadWriteCloser) {
	dec := gob.NewDecoder(conn)
	enc := gob.NewEncoder(conn)
	sending := new(sync.Mutex)
	var wg sync.WaitGroup

	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			break
		}

		svc, mtype, lookupErr := server.lookup(req.ServiceMethod)

		var argv reflect.Value
		argIsValue := false
		decodeErr := lookupErr
		if mtype != nil {
			if mtype.argType.Kind() == reflect.Ptr {
				argv = reflect.New(mtype.argType.Elem())
			} else {
				argv = reflect.New(mtype.argType)
				argIsValue = true
			}
			if derr := dec.Decode(argv.Interface()); derr != nil && decodeErr == nil {
				decodeErr = derr
			}
		} else {
			_ = discard(dec) //nolint:errcheck // unknown method: skip the arg body
		}

		if decodeErr != nil {
			server.sendResponse(sending, enc, &req, invalidReply{}, decodeErr.Error())
			continue
		}

		// argv is a pointer (reflect.New). For a value-typed arg, deref to the
		// value the method expects; for a pointer-typed arg, leave the pointer.
		if argIsValue {
			argv = argv.Elem()
		}

		wg.Add(1)
		go func(req Request, argv reflect.Value) {
			defer wg.Done()
			replyv := reflect.New(mtype.replyType.Elem())
			rets := mtype.method.Func.Call([]reflect.Value{svc.rcvr, argv, replyv})
			errmsg := ""
			if ei := rets[0].Interface(); ei != nil {
				errmsg = ei.(error).Error()
			}
			server.sendResponse(sending, enc, &req, replyv.Interface(), errmsg)
		}(req, argv)
	}

	wg.Wait()
	_ = conn.Close() //nolint:errcheck
}

type invalidReply struct{}

func (server *Server) lookup(serviceMethod string) (*service, *methodType, error) {
	dot := strings.LastIndex(serviceMethod, ".")
	if dot < 0 {
		return nil, nil, errors.New("gobrpc: service/method ill-formed: " + serviceMethod)
	}
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]

	server.mu.Lock()
	svc := server.serviceMap[serviceName]
	server.mu.Unlock()
	if svc == nil {
		return nil, nil, errors.New("gobrpc: can't find service " + serviceName)
	}
	mtype := svc.method[methodName]
	if mtype == nil {
		return nil, nil, errors.New("gobrpc: can't find method " + serviceMethod)
	}
	return svc, mtype, nil
}

func (server *Server) sendResponse(sending *sync.Mutex, enc *gob.Encoder, req *Request, reply any, errmsg string) {
	var resp Response
	resp.ServiceMethod = req.ServiceMethod
	resp.Seq = req.Seq
	resp.Error = errmsg
	if errmsg != "" {
		reply = invalidReply{}
	}
	sending.Lock()
	defer sending.Unlock()
	if err := enc.Encode(&resp); err != nil {
		return
	}
	_ = enc.Encode(reply) //nolint:errcheck
}
