#!/bin/bash

set -e

CERTS_DIR=certs
KEY_SIZE=512

mkdir -p $CERTS_DIR $CERTS_DIR/calico $CERTS_DIR/etcd

cd $CERTS_DIR || exit 1

keys=(apiserver worker calico/client etcd/server)

for key in ${keys[*]}; do
  # generate master keys
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:$KEY_SIZE -out "${key}-ca.pem"

  # generate self-signed certs and cert keys
  # -nodes: unencrypted key
  # -x509: self-signed
  openssl req -new -newkey rsa:$KEY_SIZE -days 365 -nodes -x509 -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com" -keyout "${key}-key.pem" -out "${key}.pem"
done
