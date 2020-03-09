#!/usr/bin/env bash

openssl req \
    -new \
    -x509 \
    -newkey rsa:2048 \
    -sha256 \
    -nodes \
    -keyout server.key \
    -days 3650 \
    -out server.crt \
    -config certificate.cnf
