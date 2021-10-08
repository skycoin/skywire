#!/bin/bash

authoremail=$1

usage="Usage: bash sign_deb.sh AUTHOR_EMAIL/KEY_EMAIL"

if [ -z "$1" ]
then
	echo "$usage"
	exit
fi

search_dir="./deb"
for entry in "$search_dir"/*
do
	dpkg-sig --sign builder "$entry" -k "$authoremail"
done