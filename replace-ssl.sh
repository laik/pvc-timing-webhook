#!/bin/bash

export TLS_CRT=`cat certs/webhook-crt.pem | base64 -w 0`
export TLS_KEY=`cat certs/webhook-key.pem | base64 -w 0`
# export CA_PEM=`cat certs/ca.pem | base64`

sed -i "s/caBundle\:.*$/caBundle: ${TLS_CRT}/g" manifests/webhook-deployment-local.yaml