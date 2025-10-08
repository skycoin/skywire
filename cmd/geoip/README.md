# GeoIP Service

A GeoIP lookup service that can run in two modes: CLI for command-line queries and API mode for HTTP-based lookups.

## Overview

The GeoIP service uses the GeoLite2-City database to provide IP geolocation information. It supports both command-line interface (CLI) for quick lookups and API mode for integration with other services.

## Features

- **CLI Mode**: Query IP information directly from the command line
- **API Mode**: RESTful API service for IP geolocation lookups
- **Automatic Database Download**: Database file is automatically downloaded on first run
- **Docker Support**: Fully containerized with docker-compose

## Modes

### CLI Mode

Query a specific IP address and get its geolocation information:

```bash
skywire svc geoip <ip> --db path/to/GeoLite2-City.mmdb
```

**Example:**
```bash
skywire svc geoip 8.8.8.8 --db ./data/GeoLite2-City.mmdb
```

### API Mode

Run as a web service on port 8080:

```bash
skywire svc geoip --api --db path/to/GeoLite2-City.mmdb
```

Port 8080 is default, you can change it by `--addr :<port>` command:

```bash
skywire svc geoip --api --addr :9090 path/to/GeoLite2-City.mmdb
```

**API Endpoints:**

1. **Query specific IP:**
   ```
   GET http://localhost:8080?ip=x.x.x.x
   ```
   
   Example:
   ```bash
   curl "http://localhost:8080?ip=8.8.8.8"
   ```

2. **Get visitor's IP info:**
   ```
   GET http://<geoip-address>/
   ```
   
   When accessed without the `ip` parameter, returns geolocation information for the visitor's IP address.

## Database

The service requires the **GeoLite2-City.mmdb** database file.

**Database URL:** `https://deb.skywire.dev/GeoLite2-City.mmdb`

## Requirements

The service requires the **GeoLite2-City.mmdb** database file to be present at the specified path.

## License

This service uses MaxMind's GeoLite2 database. By using this service, you agree to MaxMind's GeoLite2 End User License Agreement (EULA). 

For more information, visit: https://www.maxmind.com/en/geolite2/eula

## Support

For issues or questions, please contact the Skywire support team.