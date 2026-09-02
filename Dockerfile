FROM node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/tsconfig.json web/tsconfig.app.json web/vite.config.ts web/index.html ./
COPY web/src ./src
RUN npm ci && npm run build

FROM golang:1.27-alpine@sha256:26402d86be3d72e6a9410afa0108f03529f51f0c1b5eb7f503d0bc44cc7857ac AS go-build
WORKDIR /src
ARG APP_VERSION=dev
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN go test ./... \
  && CGO_ENABLED=0 go build -ldflags "-X main.version=${APP_VERSION}" -o /out/mailtail ./cmd/mailtail

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
LABEL org.opencontainers.image.title="MailTail" \
      org.opencontainers.image.description="Modern open-source SMTP test inbox" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.url="https://github.com/vquie/MailTail" \
      org.opencontainers.image.source="https://github.com/vquie/MailTail" \
      org.opencontainers.image.documentation="https://github.com/vquie/MailTail#readme"
RUN adduser -D -h /app mailtail
WORKDIR /app
COPY --from=go-build /out/mailtail /app/mailtail
COPY --from=web-build /src/web/dist /app/web/dist
RUN mkdir -p /data \
  && chown -R mailtail:mailtail /app /data
USER mailtail
EXPOSE 8025 8080
VOLUME ["/data"]
ENV MAILTAIL_DATA_DIR=/data
ENV MAILTAIL_HTTP_ADDR=:8080
ENV MAILTAIL_SMTP_ADDR=:8025
ENV MAILTAIL_WEB_DIR=/app/web/dist
CMD ["/app/mailtail"]
