# golang-ipc
 Golang Inter-process communication library for Window, Mac and Linux.


 ### Overview
 
 A simple to use package that uses unix sockets on Macos/Linux and named pipes on Windows to create a communication channel between two go processes.

### Intergration

As well as using this library just for go processes it was also designed to work with other languages, with the go process as the server and the other languages processing being the client.


#### NodeJs

I currently use this library to comunicate between a ElectronJs GUI and a go program.

https://github.com/james-barrow/node-ipc-client

#### Python 

To do

## Usage

Create a server with the default configuation and start listening for the client:

```go

	sc, err := ipc.StartServer("<name of socket or pipe>", nil)
	if err != nil {
		log.Println(err)
		return
	}

```

Create a client and connect to the server:

```go

	cc, err := ipc.StartClient("<name of socket or pipe>", nil)
	if err != nil {
		log.Println(err)
		return
	}

```
Read and write data to the connection:

```go
        // write data
        _ = sc.Write(1, []byte("Message from server"))
        
        _ = cc.Write(5, []byte("Message from client"))


        // Read data
        for {
            
            dataType, data, err := sc.Read()

            if err == nil {
                log.Println("Server recieved: "+string(data)+" - Message type: ", dataType)
            } else {
                log.Println(err)
                break
            }
	    }


        for {
            
            dataType, data, err := cc.Read()

            if err == nil {
                log.Println("Client recieved: "+string(data)+" - Message type: ", dataType)     
            } else {
                log.Println(err)
                break
            }
	    }

```

 ### Encryption

 By default the connection established will be encypted, ECDH384 is used for the key exchange and AES 256 GCM is used for the cipher.

 Encryption can be swithed off by passing in a custom configuation to the server & client start functions.

```go
    
    config := &ipc.ServerConfig{Encryption: false}
	sc, err := ipc.StartServer("<name of socket or pipe>", config)

```

 ### Unix Socket Permissions

 Under most configurations, a socket created by a user will by default not be writable by another user, making it impossible for the client and server to communicate if being run by separate users.

 The permission mask can be dropped during socket creation by passing custom configuration to the server start function.  **This will make the socket writable by any user.**

```go

	config := &ipc.ServerConfig{UnmaskPermissions: true}
	sc, err := ipc.StartServer("<name of socket or pipe>", config)

```
 Note: Tested on Linux, not tested on Mac, not implemented on Windows.
 


 ### Testing

 The package has been tested on Mac, Windows and Linux and has extensive test coverage.

### Licence

MIT
