FROM node:24-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/tsconfig.json web/tsconfig.app.json web/vite.config.ts web/index.html ./
COPY web/src ./src
COPY web/tests ./tests
RUN npm ci && npm test && npm run build

FROM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS go-build
WORKDIR /src
ARG APP_VERSION=dev
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN go test ./... \
  && CGO_ENABLED=0 go build -ldflags "-X main.version=${APP_VERSION}" -o /out/mailtail ./cmd/mailtail

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
LABEL org.opencontainers.image.title="MailTail" \
      org.opencontainers.image.description="Modern open-source SMTP test inbox" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.url="https://github.com/vquie/MailTail" \
      org.opencontainers.image.source="https://github.com/vquie/MailTail" \
      org.opencontainers.image.documentation="https://github.com/vquie/MailTail#readme"
RUN apk add --no-cache su-exec \
  && addgroup -S -g 10001 mailtail \
  && adduser -S -D -h /app -u 10001 -G mailtail mailtail
WORKDIR /app
COPY --from=go-build /out/mailtail /app/mailtail
COPY --from=web-build /src/web/dist /app/web/dist
COPY docker-entrypoint.sh /usr/local/bin/mailtail-entrypoint
RUN mkdir -p /data \
  && chown -R mailtail:mailtail /app /data \
  && chmod 0755 /usr/local/bin/mailtail-entrypoint
EXPOSE 8025 8080
VOLUME ["/data"]
ENV MAILTAIL_DATA_DIR=/data
ENV MAILTAIL_HTTP_ADDR=:8080
ENV MAILTAIL_SMTP_ADDR=:8025
ENV MAILTAIL_WEB_DIR=/app/web/dist
# Root is used only to migrate mounted volume ownership; the entrypoint drops to UID/GID 10001 before starting MailTail.
# hadolint ignore=DL3002
USER 0:0
ENTRYPOINT ["/usr/local/bin/mailtail-entrypoint"]
CMD ["/app/mailtail"]
