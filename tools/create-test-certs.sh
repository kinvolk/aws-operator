#!/bin/bash
set -e

CERTS_DIR=certs
ALGO=RSA
KEY_SIZE=512
DAYS=365

mkdir -p $CERTS_DIR $CERTS_DIR/calico $CERTS_DIR/etcd

cd $CERTS_DIR || exit 1

keys=(apiserver worker calico/client etcd/server)

# generate master keys
for key in ${keys[*]}; do
  openssl genpkey -algorithm $ALGO -pkeyopt rsa_keygen_bits:$KEY_SIZE -out "${key}-ca.pem"
done

# generate self-signed certs and cert keys
# -nodes: unencrypted key
# -x509: self-signed
openssl req -new -newkey rsa:$KEY_SIZE -days $DAYS -nodes -x509 -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com" -keyout apiserver-key.pem -out apiserver.pem
openssl req -new -newkey rsa:$KEY_SIZE -days $DAYS -nodes -x509 -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com" -keyout worker-key.pem -out worker.pem
openssl req -new -newkey rsa:$KEY_SIZE -days $DAYS -nodes -x509 -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com" -keyout calico/client-key.pem -out calico/client.pem
openssl req -new -newkey rsa:$KEY_SIZE -days $DAYS -nodes -x509 -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com" -keyout etcd/server-key.pem -out etcd/server.pem
