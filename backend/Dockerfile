# The backend, built statically and run on Alpine.
#
# Note the git packages: Alpine's `git` does not contain everything git's HTTP
# side needs — `git-daemon` carries the rest. This server speaks smart HTTP
# itself (it runs upload-pack and receive-pack directly), so only `git` is
# strictly required; git-daemon is installed anyway so the usual tooling is
# there if you ever want it.

FROM golang:1.26-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache git git-daemon ca-certificates tzdata openssh-client su-exec \
    && adduser -D -u 10001 app
ENV TZ=Europe/Berlin
COPY --from=build /out/server /usr/local/bin/server
COPY entrypoint.sh /usr/local/bin/entrypoint.sh

# Both directories are bind-mounted from the host in docker-compose.yml. In the
# image they would be gone after every deploy. The entrypoint hands them to the
# app user before dropping privileges, whatever the host's ownership is.
RUN mkdir -p /srv/git /srv/data && chown -R app:app /srv/git /srv/data \
    && chmod +x /usr/local/bin/entrypoint.sh
WORKDIR /srv
EXPOSE 5000
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s \
  CMD wget -q --spider http://127.0.0.1:5000/health || exit 1
ENTRYPOINT ["/usr/local/bin/entrypoint.sh", "/usr/local/bin/server"]
