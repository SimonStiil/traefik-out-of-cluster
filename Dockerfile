FROM scratch

ARG TARGETARCH

WORKDIR /app
COPY ca-certificates.crt /etc/ssl/certs/
COPY traefik-out-of-cluster-${TARGETARCH} /usr/bin/traefik-out-of-cluster
ENTRYPOINT ["traefik-out-of-cluster"]