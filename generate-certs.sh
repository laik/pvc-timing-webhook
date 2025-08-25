#!/bin/bash

# 生成TLS证书脚本
# 用于PVC Timing Webhook的HTTPS配置

set -e

# 配置变量
WEBHOOK_NAME="kube-system"
WEBHOOK_NAMESPACE="kube-system"
WEBHOOK_SERVICE="${WEBHOOK_NAME}.${WEBHOOK_NAMESPACE}.svc"
WEBHOOK_SERVICE_LOCAL="${WEBHOOK_NAME}-local.${WEBHOOK_NAMESPACE}-local.svc"

# 创建证书目录
mkdir -p certs

echo "Generating TLS certificates for PVC Timing Webhook..."

# 生成私钥
openssl genrsa -out certs/webhook-key.pem 2048

# 生成证书签名请求 (CSR)
cat > certs/webhook-csr.conf << EOF
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
[req_distinguished_name]
[ v3_req ]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names
[alt_names]
DNS.1 = ${WEBHOOK_SERVICE}
DNS.2 = ${WEBHOOK_SERVICE_LOCAL}
DNS.3 = ${WEBHOOK_NAME}
DNS.4 = ${WEBHOOK_NAME}-local
DNS.5 = localhost
IP.1 = 127.0.0.1
IP.2 = 192.168.5.200
EOF

# 生成CSR
openssl req -new -key certs/webhook-key.pem -subj "/CN=${WEBHOOK_SERVICE}" -out certs/webhook.csr -config certs/webhook-csr.conf

# 生成自签名证书
openssl x509 -req -in certs/webhook.csr -signkey certs/webhook-key.pem -out certs/webhook-crt.pem -days 365 -extensions v3_req -extfile certs/webhook-csr.conf

# 生成base64编码的证书和私钥（用于Kubernetes Secret）
echo "Generating base64 encoded certificates..."

# 创建证书的base64编码
CERT_B64=$(cat certs/webhook-crt.pem | base64 | tr -d '\n')
KEY_B64=$(cat certs/webhook-key.pem | base64 | tr -d '\n')

# 创建Kubernetes Secret YAML
cat > certs/webhook-tls-secret.yaml << EOF
apiVersion: v1
kind: Secret
metadata:
  name: webhook-tls
  namespace: ${WEBHOOK_NAMESPACE}
type: kubernetes.io/tls
data:
  tls.crt: ${CERT_B64}
  tls.key: ${KEY_B64}
---
apiVersion: v1
kind: Secret
metadata:
  name: webhook-tls-local
  namespace: ${WEBHOOK_NAMESPACE}-local
type: kubernetes.io/tls
data:
  tls.crt: ${CERT_B64}
  tls.key: ${KEY_B64}
EOF

echo "TLS certificates generated successfully!"
echo "Files created:"
echo "  - certs/webhook-key.pem (private key)"
echo "  - certs/webhook-crt.pem (certificate)"
echo "  - certs/webhook-tls-secret.yaml (Kubernetes Secret)"
echo ""
echo "To deploy the certificates:"
echo "  kubectl apply -f certs/webhook-tls-secret.yaml"
echo ""
echo "Certificate details:"
openssl x509 -in certs/webhook-crt.pem -text -noout | grep -E "(Subject:|DNS:|IP Address:)"
