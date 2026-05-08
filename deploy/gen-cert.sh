#!/usr/bin/env bash
set -euo pipefail

IP_ADDRESS=${1:?usage: gen-cert.sh <server-ip> [output-dir]}
OUTPUT_DIR=${2:-/opt/shared-clipboard/tls}

mkdir -p "$OUTPUT_DIR"
openssl req -x509 -nodes -newkey rsa:2048 -sha256 -days 3650 \
  -subj "/CN=${IP_ADDRESS}" \
  -addext "subjectAltName = IP:${IP_ADDRESS}" \
  -keyout "$OUTPUT_DIR/key.pem" \
  -out "$OUTPUT_DIR/cert.pem"
chmod 600 "$OUTPUT_DIR/key.pem"
chmod 644 "$OUTPUT_DIR/cert.pem"
