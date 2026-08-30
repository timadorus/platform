#!/bin/bash
#

# Open the web UI (one port-forward covers the app and both APIs):
kubectl port-forward --namespace traefik svc/traefik 8080:80 &

# Zitadel (needed for the login redirect above to resolve):
kubectl port-forward --namespace zitadel svc/zitadel 8084:8080 &
kubectl port-forward --namespace zitadel svc/zitadel-login 8085:3000 &
