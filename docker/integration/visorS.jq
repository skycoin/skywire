# visor-S as the deployment host (make e2e-config): the dmsg server folded in
# under its own key, and every deployment service in the visor's process
# under the service's own key. Each block's addr serves the suite's curls.
# e2edmsg is the e2e dmsg network; a service without it dials prod's.
def e2edmsg: {"sessions_count": 1, "discovery_dmsg": "dmsg://02857a87410b3709eddb99bc00508cc7cfeb588d5ed87e3ef9d2aac838ad49470a:80", "servers": [{"version": "", "sequence": 0, "timestamp": 0, "static": "035915c609f71d0c7df27df85ec698ceca0cb262590a54f732e3bbd0cc68d89282", "server": {"address": "174.0.0.17:8080", "availableSessions": 0}}]};
.launcher.apps |= map(.auto_start = false)
| .dmsg.server = {"enabled": true, "config_path": "/release/dmsg-server.json"}
| .embedded_services = [
  {"type": "dmsg-discovery", "name": "dmsgd", "secret_key": "b3f6706cb72215d3873ef92cc0c6037a47fe651112b1685017d6347eed0fb714", "addr": ":9090", "redis": "redis://redis:6379/0", "testing": true, "dmsg_servers": [{"version":"","sequence":0,"timestamp":0,"static":"035915c609f71d0c7df27df85ec698ceca0cb262590a54f732e3bbd0cc68d89282","server":{"address":"174.0.0.17:8080","availableSessions":0}}]},
  {"type": "transport-discovery", "name": "tpd", "secret_key": "4f86dccdf89cee59129c0d7368bb3a1978f8acaf859ede2be2089b1aee1feebd", "dmsg": e2edmsg, "addr": ":9094", "redis": "redis://redis:6379/1", "entry_timeout": "2m", "uptime_db": "", "store_data_path": "/var/lib/skywire/tpd/bandwidth"},
  {"type": "route-finder", "name": "rf", "secret_key": "196763a99ee79263bd5b943f0f52333c999854264243e5acb547f420e1b15af0", "dmsg": e2edmsg, "addr": ":9092", "redis": "redis://redis:6379/1"},
  {"type": "service-discovery", "name": "sd", "secret_key": "1ef988de45d6056bb47a5dec9aa82fa58a3068a88e5c4a25ea1dc3ef42f88913", "dmsg": e2edmsg, "addr": ":9091", "redis": "redis://redis:6379/2", "entry_timeout": "2m"},
  {"type": "address-resolver", "name": "ar", "secret_key": "9148ff4d4595de30da39e7d9d9469a07189fadb748dfc10456075138fca4f75f", "dmsg": e2edmsg, "addr": ":9093", "udp_addr": ":9093", "public_udp_addr": "174.0.0.17:9093", "redis": "redis://redis:6379/3", "entry_timeout": "2m"},
  {"type": "setup-node", "name": "sn", "config_path": "/release/setup-node.json"},
  {"type": "transport-setup", "name": "tps", "config_path": "/release/transport-setup.json"},
  {"type": "stun-server", "name": "stun", "primary_ip": "173.0.0.17", "secondary_ip": "174.0.0.17", "port": 3478, "alt_port": 3479}
]
