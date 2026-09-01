# SABI-VIP-OK: one container, zero manual steps.
#   - SabiTun tunnel server (custom Noise_IK protocol)
#   - Xray-core running VLESS + VMess + Trojan + Shadowsocks
#   - nginx path-routing all of them on Railway's single public $PORT
#   - a single /setup page listing every ready-to-import config
#
# Deploy: create an empty Railway project -> "Deploy from GitHub repo" ->
# point it at this repo -> generate a public domain. That's it. Everything
# else (installing dependencies, building, generating credentials, writing
# configs) happens automatically in entrypoint.sh on container boot.

FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum* ./
COPY . .
RUN go mod tidy
RUN go build -o /sabitun-server ./server

FROM alpine:3.20
RUN apk add --no-cache bash nginx curl unzip openssl ca-certificates && \
    curl -sfL -o /tmp/xray.zip https://github.com/XTLS/Xray-core/releases/latest/download/Xray-linux-64.zip && \
    unzip -o /tmp/xray.zip -d /usr/local/bin xray && \
    chmod +x /usr/local/bin/xray && \
    rm -f /tmp/xray.zip

COPY --from=build /sabitun-server /usr/local/bin/sabitun-server
COPY xray/config.template.json /etc/xray/config.template.json
COPY nginx.conf.template /etc/nginx/nginx.conf.template
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh /usr/local/bin/sabitun-server

EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
