#!/usr/bin/env bash
# Explicit local test helper. Never installs trust, edits firewalls, or opens production DBs.
set -euo pipefail
umask 077
if [[ $# -lt 1 || $# -gt 2 || (${2:-} != '' && ${2:-} != '--prepare-only') ]]; then
  echo 'Usage: bash scripts/sales-lan-test.sh <computer LAN IPv4> [--prepare-only]' >&2
  exit 2
fi
sales_ip=$1
python3 - "$sales_ip" <<'PY'
import ipaddress, sys
ip = ipaddress.IPv4Address(sys.argv[1])
allowed = any(ip in ipaddress.ip_network(n) for n in ('10.0.0.0/8','172.16.0.0/12','192.168.0.0/16'))
if not allowed:
    sys.exit('Use the computer private LAN IPv4, not localhost or a public address.')
PY
sales_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
sales_dir="$sales_root/.sales-lan-test"
mkdir -p "$sales_dir"
chmod 700 "$sales_dir"
if [[ ! -e "$sales_dir/ca.crt" && ! -e "$sales_dir/ca.key" ]]; then
  openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 30 \
    -subj '/CN=Steam Sales LAN Test CA' \
    -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
    -addext 'keyUsage=critical,keyCertSign,cRLSign' \
    -keyout "$sales_dir/ca.key" -out "$sales_dir/ca.crt" 2>"$sales_dir/cert-generation.log"
fi
if [[ ! -f "$sales_dir/ca.crt" || ! -f "$sales_dir/ca.key" ]]; then
  echo 'Incomplete test CA. Restore the matching CA files before continuing.' >&2; exit 1
fi
openssl x509 -checkend 86400 -noout -in "$sales_dir/ca.crt"
cat > "$sales_dir/server.ext" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=IP:$sales_ip,IP:127.0.0.1,DNS:localhost
EOF
openssl req -new -newkey rsa:2048 -nodes -sha256 -subj '/CN=Steam Sales LAN Test' \
  -keyout "$sales_dir/server.key" -out "$sales_dir/server.csr" 2>"$sales_dir/cert-generation.log"
openssl x509 -req -in "$sales_dir/server.csr" -CA "$sales_dir/ca.crt" -CAkey "$sales_dir/ca.key" \
  -CAcreateserial -days 7 -sha256 -extfile "$sales_dir/server.ext" -out "$sales_dir/server.crt"
openssl verify -CAfile "$sales_dir/ca.crt" -verify_ip "$sales_ip" "$sales_dir/server.crt"
printf 'Phone URL: https://%s:8443\nPublic CA to install on phone: %s/ca.crt\n' "$sales_ip" "$sales_dir"
openssl x509 -in "$sales_dir/ca.crt" -noout -fingerprint -sha256
if [[ ${2:-} == '--prepare-only' ]]; then exit 0; fi
cd "$sales_root"
go build -o "$sales_dir/monitor" ./cmd/monitor
if [[ -z ${SALES_PASSWORD:-} ]]; then
  read -r -s -p 'Test login password (at least 16 characters): ' SALES_PASSWORD
  printf '\n'
fi
export SALES_PASSWORD
exec "$sales_dir/monitor" -mode sales -db "$sales_dir/sales-test.db" \
  -sales-listen :8443 -sales-origin "https://$sales_ip:8443" \
  -sales-tls-cert "$sales_dir/server.crt" -sales-tls-key "$sales_dir/server.key"
