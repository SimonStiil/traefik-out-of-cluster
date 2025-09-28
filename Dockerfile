FROM scratch

ARG TARGETARCH

WORKDIR /app
COPY traefik-out-of-cluster-${TARGETARCH} /usr/bin/traefik-out-of-cluster
ENTRYPOINT ["traefik-out-of-cluster"]