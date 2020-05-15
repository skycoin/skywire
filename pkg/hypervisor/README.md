# Config

- `public_key` ([PubKey](#PubKey))
- `secret_key` ([SecKey](#SecKey))
- `db_path` (string)
- `enable_auth` (bool)
- `cookies` ([CookieConfig](#CookieConfig))
- `dmsg_discovery` (string)
- `dmsg_port` ([uint16](#uint16))
- `http_addr` (string)
- `enable_tls` (bool)
- `tls_cert_file` (string)
- `tls_key_file` (string)


# CookieConfig

- `hash_key` ([Key](#Key))
- `block_key` ([Key](#Key))
- `expires_duration` ([Duration](#Duration))
- `path` (string)
- `domain` (string)
- `-` (bool)
