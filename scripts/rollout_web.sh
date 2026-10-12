#!/bin/sh
# Executed over SSH by main's workflow, never by local INFRA preparation.
set -eu

deployment="$(kubectl -n default get deployment matrixqc --ignore-not-found -o name)"
if [ -z "$deployment" ]; then
    echo 'matrixqc is not installed; images are published. Merge the infrastructure PR after Gate 2.'
    exit 0
fi

kubectl -n default rollout restart deployment/matrixqc
kubectl -n default rollout status deployment/matrixqc --timeout=120s
