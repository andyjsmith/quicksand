# The Quicksand server image. It packages a server binary built beforehand
# rather than building one, so it's the same binary as in the release
# packages. To build it locally:
#
#   ./scripts/build-server.sh && docker build -t quicksand .
#
# CI publishes it as ghcr.io/andyjsmith/quicksand. compose.yml shows how to
# run it, and docs/deploy.md has the details.
FROM alpine:3

RUN apk add --no-cache tzdata && \
	adduser -D -H -u 10001 -h /var/lib/quicksand -s /sbin/nologin quicksand && \
	install -d -m 0700 -o quicksand -g quicksand /var/lib/quicksand

COPY dist/server/quicksand /usr/bin/quicksand

ARG VERSION=dev
LABEL org.opencontainers.image.title="Quicksand" \
	org.opencontainers.image.description="Self-service sandbox VMs on Proxmox VE" \
	org.opencontainers.image.source="https://github.com/andyjsmith/quicksand" \
	org.opencontainers.image.licenses="MIT" \
	org.opencontainers.image.version="$VERSION"

# The configuration (config.json, the icons folder next to it and any TLS
# certificate) is mounted at /etc/quicksand; the database is kept in the
# working directory.
USER quicksand
WORKDIR /var/lib/quicksand
VOLUME /var/lib/quicksand
EXPOSE 8484
ENTRYPOINT ["quicksand"]
CMD ["serve", "-config", "/etc/quicksand/config.json"]
