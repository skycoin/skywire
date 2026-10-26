# geopatch

Corrects the country of visor IPs in the embedded GeoLite2-City database
(`pkg/geoip/GeoLite2-City.mmdb.gz`). The reward calculation scales each
visor's share by its country's IP count, so a wrong country changes the reward.

Each IP gets three votes: the GeoLite2 data, DB-IP's free lite data, and the
regional registries' delegation files. When two of them agree on a country
GeoLite2 does not give, the IP gets a `/32` record holding that country.
Nothing else in the database changes. IPs with no agreement are reported and
left alone. The registry files name the country of the block's holder, not
where a host is, which is why they only count as one vote.

It is a separate module so its dependencies stay out of the main one. Run it
again after replacing the GeoLite2 data, since the new data may still be wrong
about some visor IPs.

## Inputs

The visor IPs, from the reward system's surveys (run in its working directory):

```
cat log_backups/*/node-info.json | grep -oE '"ip_address": *"[0-9.]+"' | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | sort -u > ips.txt
```

DB-IP's country lite data, licensed CC BY 4.0 (replace the month):

```
curl -LO https://download.db-ip.com/free/dbip-country-lite-2026-10.mmdb.gz && gunzip dbip-country-lite-2026-10.mmdb.gz
```

The registry delegation files, in one directory:

```
mkdir rir && cd rir
curl -LO https://ftp.apnic.net/stats/apnic/delegated-apnic-extended-latest
curl -LO https://ftp.ripe.net/pub/stats/ripencc/delegated-ripencc-extended-latest
curl -LO https://ftp.arin.net/pub/stats/arin/delegated-arin-extended-latest
curl -LO https://ftp.lacnic.net/pub/stats/lacnic/delegated-lacnic-extended-latest
curl -LO https://ftp.afrinic.net/pub/stats/afrinic/delegated-afrinic-extended-latest
```

## Run

From the repository root, with the inputs above in `/tmp/geo`:

```
gunzip -c pkg/geoip/GeoLite2-City.mmdb.gz > /tmp/geo/GeoLite2-City.mmdb
go -C cmd/geopatch run . -n -in /tmp/geo/GeoLite2-City.mmdb -dbip /tmp/geo/dbip-country-lite-2026-10.mmdb -rir /tmp/geo/rir -ips /tmp/geo/ips.txt
go -C cmd/geopatch run . -in /tmp/geo/GeoLite2-City.mmdb -out /tmp/geo/patched.mmdb -dbip /tmp/geo/dbip-country-lite-2026-10.mmdb -rir /tmp/geo/rir -ips /tmp/geo/ips.txt
gzip -9 -n -c /tmp/geo/patched.mmdb > pkg/geoip/GeoLite2-City.mmdb.gz
```

`-n` only reports what would change. Writing takes a few minutes. Running it
again on the patched file should report `patched=0`.

Patched IPs carry a country and continent but no city or coordinates.
