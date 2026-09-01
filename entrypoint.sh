#!/bin/bash
# SABI-VIP-OK entrypoint. Runs entirely automatically on container boot:
#   1. Starts the SabiTun tunnel server (local only, port 8081).
#   2. Generates (or reuses, if you set env vars) credentials for VLESS,
#      VMess, Trojan, and Shadowsocks, writes the Xray config, starts Xray.
#   3. Builds nginx.conf (path-routes all 5 protocols on Railway's $PORT).
#   4. Writes /var/www/setup-data.json (safely, via jq) with every config;
#      the static, already-baked-in /var/www/setup.html renders it with a
#      polished UI and one-click copy buttons. This is "the one link".
#   5. Starts nginx in the foreground (the container's main process).
#
# You do not need to run or configure anything by hand. Deploy this repo
# on Railway (empty project -> "Deploy from GitHub repo" -> this repo),
# generate a public domain for the service, then open:
#   https://<your-service-domain>/setup
set -e

PORT="${PORT:-8080}"
DOMAIN="${RAILWAY_PUBLIC_DOMAIN:-${RAILWAY_STATIC_URL:-localhost:$PORT}}"

echo "================================================================"
echo " SABI-VIP-OK starting up"
echo " Public domain detected: $DOMAIN"
echo "================================================================"

# ---------------------------------------------------------------------
# 1. Start the SabiTun server (local-only port, nginx proxies /connect)
# ---------------------------------------------------------------------
export SABITUN_LOCAL_ADDR="127.0.0.1:8081"
/usr/local/bin/sabitun-server > /var/log/sabitun.log 2>&1 &

echo "Waiting for SabiTun server to report its public key..."
SABITUN_PUBKEY=""
for i in $(seq 1 20); do
  SABITUN_PUBKEY=$(grep -o 'public key[^:]*: [^ ]*' /var/log/sabitun.log 2>/dev/null | tail -1 | awk '{print $NF}')
  if [ -n "$SABITUN_PUBKEY" ]; then
    break
  fi
  sleep 0.5
done
if [ -z "$SABITUN_PUBKEY" ]; then
  echo "WARNING: could not read SabiTun public key from logs after 10s; check /var/log/sabitun.log"
  SABITUN_PUBKEY="(unavailable -- check container logs)"
fi
echo "SabiTun public key: $SABITUN_PUBKEY"

# ---------------------------------------------------------------------
# 2. Credentials for the 4 extra protocols (reuse env vars if you set
#    your own, otherwise generate fresh random ones every boot)
# ---------------------------------------------------------------------
gen_uuid() {
  if [ -r /proc/sys/kernel/random/uuid ]; then
    cat /proc/sys/kernel/random/uuid
  elif command -v uuidgen >/dev/null 2>&1; then
    uuidgen
  else
    hex=$(openssl rand -hex 16)
    printf '%s-%s-4%s-a%s-%s\n' "${hex:0:8}" "${hex:8:4}" "${hex:13:3}" "${hex:17:3}" "${hex:20:12}"
  fi
}

VLESS_ID="${VLESS_ID:-$(gen_uuid)}"
VMESS_ID="${VMESS_ID:-$(gen_uuid)}"
TROJAN_PW="${TROJAN_PW:-$(openssl rand -base64 16 | tr -dc 'A-Za-z0-9' | head -c22)}"
SS_METHOD="chacha20-ietf-poly1305"
SS_PW="${SS_PW:-$(openssl rand -base64 16 | tr -dc 'A-Za-z0-9' | head -c22)}"

sed \
  -e "s/__VLESS_ID__/$VLESS_ID/" \
  -e "s/__VMESS_ID__/$VMESS_ID/" \
  -e "s/__TROJAN_PW__/$TROJAN_PW/" \
  -e "s/__SS_PW__/$SS_PW/" \
  /etc/xray/config.template.json > /etc/xray/config.json

/usr/local/bin/xray run -c /etc/xray/config.json > /var/log/xray.log 2>&1 &

# ---------------------------------------------------------------------
# 3. nginx config
# ---------------------------------------------------------------------
sed "s/__PORT__/$PORT/" /etc/nginx/nginx.conf.template > /etc/nginx/nginx.conf

# ---------------------------------------------------------------------
# 4. Build /var/www/setup-data.json (safely, via jq -- no manual string
#    escaping). The polished UI itself (/var/www/setup.html) is static
#    and already baked into the image; it just fetches this JSON.
# ---------------------------------------------------------------------
VMESS_JSON=$(jq -n \
  --arg ps "SABI-VMess" --arg add "$DOMAIN" --arg id "$VMESS_ID" --arg host "$DOMAIN" \
  '{v:"2",ps:$ps,add:$add,port:"443",id:$id,aid:"0",scy:"auto",net:"ws",type:"none",host:$host,path:"/vmess",tls:"tls",sni:"",alpn:""}')
VMESS_LINK="vmess://$(printf '%s' "$VMESS_JSON" | base64 | tr -d '\n')"
SS_USERINFO=$(printf '%s:%s' "$SS_METHOD" "$SS_PW" | base64 | tr -d '\n')

VLESS_LINK="vless://${VLESS_ID}@${DOMAIN}:443?type=ws&security=tls&path=%2Fxray&host=${DOMAIN}#SABI-VLESS"
TROJAN_LINK="trojan://${TROJAN_PW}@${DOMAIN}:443?security=tls&type=ws&path=%2Ftrojan&host=${DOMAIN}#SABI-Trojan"

SS_JSON_CONFIG=$(jq -n \
  --arg address "$DOMAIN" --arg method "$SS_METHOD" --arg password "$SS_PW" --arg host "$DOMAIN" \
  '{log:{loglevel:"warning"},
    inbounds:[{listen:"127.0.0.1",port:10808,protocol:"socks",settings:{udp:true}}],
    outbounds:[{protocol:"shadowsocks",
      settings:{servers:[{address:$address,port:443,method:$method,password:$password}]},
      streamSettings:{network:"ws",security:"tls",
        wsSettings:{path:"/ss",headers:{Host:$host}},
        tlsSettings:{serverName:$host}}}]}')

jq -n \
  --arg domain "$DOMAIN" \
  --arg sabitunUrl "wss://$DOMAIN/connect" \
  --arg sabitunPubkey "$SABITUN_PUBKEY" \
  --arg vless "$VLESS_LINK" \
  --arg vmess "$VMESS_LINK" \
  --arg trojan "$TROJAN_LINK" \
  --arg shadowsocksConfig "$SS_JSON_CONFIG" \
  '{
    domain: $domain,
    sabitun: { url: $sabitunUrl, pubkey: $sabitunPubkey },
    vless: $vless,
    vmess: $vmess,
    trojan: $trojan,
    shadowsocksConfig: $shadowsocksConfig
  }' > /var/www/setup-data.json

echo "================================================================"
echo " Setup complete. Open this URL for every config, all in one page:"
echo "   https://$DOMAIN/setup"
echo "================================================================"

# ---------------------------------------------------------------------
# 5. nginx as the container's main (foreground) process
# ---------------------------------------------------------------------
exec nginx -g "daemon off;"
