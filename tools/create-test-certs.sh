#!/bin/bash
set -e

CERTS_DIR=certs
KEY_SIZE=4096
DAYS=365
ALGO=RSA

mkdir $CERTS_DIR
mkdir $CERTS_DIR/calico
mkdir $CERTS_DIR/etcd

cd $CERTS_DIR || exit 1

keys=(apiserver worker calico/client etcd/server)

# generate master key
for key in ${keys[*]}; do
  openssl genpkey -algorithm $ALGO -out "${key}-ca.pem"
  openssl genpkey -algorithm $ALGO -out "${key}-ca.pem"
  openssl genpkey -algorithm $ALGO -out "${key}-ca.pem"
  openssl genpkey -algorithm $ALGO -out "${key}-ca.pem"
done

openssl req -new -newkey rsa:$KEY_SIZE -days $DAYS -nodes -x509 -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com" -keyout apiserver-key.pem -out apiserver.pem
openssl req -new -newkey rsa:$KEY_SIZE -days $DAYS -nodes -x509 -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com" -keyout worker-key.pem -out worker.pem
openssl req -new -newkey rsa:$KEY_SIZE -days $DAYS -nodes -x509 -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com" -keyout calico/client-key.pem -out calico/client.pem
openssl req -new -newkey rsa:$KEY_SIZE -days $DAYS -nodes -x509 -subj "/C=US/ST=Denial/L=Springfield/O=Dis/CN=www.example.com" -keyout etcd/server-key.pem -out etcd/server.pem
