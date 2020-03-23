#!/bin/sh

cd ./static/skywire-manager-src
npm install
cd ./ssl && sh ./generate.sh
npm run start