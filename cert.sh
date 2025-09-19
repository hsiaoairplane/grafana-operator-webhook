#!/bin/bash
set -e

# Define variables
NAMESPACE=grafana-operator
SECRET_NAME=webhook-tls-secret
WEBHOOK_SERVICE=webhook
CERT_DIR="./certs"

# Create certificate directory
mkdir -p ${CERT_DIR}

# Step 1: Generate a CA Key and Certificate
openssl genrsa -out ${CERT_DIR}/ca.key 2048
openssl req -x509 -new -nodes -key ${CERT_DIR}/ca.key -subj "/CN=Kubernetes CA" -days 365 -out ${CERT_DIR}/ca.crt

# Step 2: Generate a Private Key for the Webhook Server
openssl genrsa -out ${CERT_DIR}/tls.key 2048

# Step 3: Generate a CSR (Certificate Signing Request) for the Webhook
openssl req -new -key ${CERT_DIR}/tls.key -subj "/CN=${WEBHOOK_SERVICE}.${NAMESPACE}.svc" -out ${CERT_DIR}/tls.csr

# Step 4: Sign the CSR with the CA to Generate the TLS Certificate
openssl x509 -req -in ${CERT_DIR}/tls.csr -CA ${CERT_DIR}/ca.crt -CAkey ${CERT_DIR}/ca.key -CAcreateserial -out ${CERT_DIR}/tls.crt -days 365 -sha256

# Step 5: Create a Kubernetes Secret with the Generated TLS Certificates
kubectl create secret tls ${SECRET_NAME} \
  --cert=${CERT_DIR}/tls.crt \
  --key=${CERT_DIR}/tls.key \
  -n ${NAMESPACE}

# Step 6: Apply the CA Bundle to the Webhook Configuration
CA_BUNDLE=$(cat ${CERT_DIR}/ca.crt | base64 | tr -d '\n')
echo "CA_BUNDLE: ${CA_BUNDLE}"

echo "TLS certificates and CA have been created successfully!"
