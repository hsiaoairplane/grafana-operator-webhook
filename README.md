# Grafana Operator CR Webhook

## Overview

The **Grafana Operator CR Webhook** is a Kubernetes admission webhook designed to optimize the handling of ArgoCD `Application` resources by filtering out unnecessary updates to reduce API server load and ETCD database growth.

### Benefits

1. **Reduces API Server Load**: Prevents frequet PUT API calls.
2. **Optimizes ETCD Storage**: Minimizes unnecessary revision history storage in ETCD.

## Deployment

```console
./cert.sh

kubectl apply -f webhook-deployment.yaml
kubectl apply -f webhook-validatingwebhookconfiguration.yaml

kubectl -n grafana-operator patch validatingwebhookconfiguration application-admission-webhook \
  --type='json' \
  -p="[{
    \"op\": \"replace\",
    \"path\": \"/webhooks/0/clientConfig/caBundle\",
    \"value\": \"$(cat certs/ca.crt | base64 | tr -d '\n')\"
  }]"
```
