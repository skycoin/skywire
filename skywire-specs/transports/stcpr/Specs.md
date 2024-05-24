# STCP transport with address resolving

STCPR transport work the same way as STCP, 
but uses the address-resolver service instead of PK table to determine an address by a PK. 

### STCPR description

Address-resolver has the following HTTP methods for resolving PKs to IPs:

- `POST` `/bind/stcpr`

It is used to bind PKs that visors send on start with their addresses. It requires PK authorization.

The request format is a JSON with a port visor listens on and with a list of visor local addresses.

- `GET` `/resolve/stcpr/{pk}`

It is used by dialing visor to resolve public key to address of dialed visor. It requires PK authorization.

- `/security/nonces/`

It is used by `httpauth` middleware for public key authorization for both binding and resolving addresses.
