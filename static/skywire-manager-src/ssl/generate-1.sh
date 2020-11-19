#!/usr/bin/env bash

openssl req \
    -new \
    -x509 \
    -newkey rsa:2048 \
    -sha256 \
    -nodes \
    -keyout /opt/skywire/ssl/key.pem \
    -days 3650 \
    -out /opt/skywire/ssl/cert.pem \
    -config /opt/skywire/ssl/certificate.cnf
