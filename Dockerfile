FROM scratch
WORKDIR /app
COPY traefik-out-of-cluster /usr/bin/
ENTRYPOINT ["traefik-out-of-cluster"]