# MailTail

MailTail is a modern open-source SMTP test inbox focused on mail infrastructure testing. This MVP accepts SMTP traffic, stores full RFC822 messages and session metadata, exposes a REST API, and ships with a React-based web UI.

## Features

- SMTP server on port `8025`
- Web UI and REST API on port `8080`
- SQLite persistence with surviving container restarts
- MIME parsing for text, HTML, and attachments
- Search over subject, sender, and recipient
- Full raw message storage
- Extensible SMTP response policy interface for future MailFail behavior

## Project Structure

```text
cmd/mailtail
internal/api
internal/models
internal/parser
internal/smtpserver
internal/storage
web
```

## Local Development

### Requirements

- Go 1.24+
- Node.js 22+
- Docker (for `make lint` with MegaLinter)

### Make targets

```bash
cp .env.example .env
make install
make test
make lint
make build
make run
make docker-run
```

`make lint` runs MegaLinter in Docker. `make lint-fix` enables automatic fixes where supported by the active linters.
If `.env` exists in the project root, `make run` and `make docker-run` load it automatically.

### Start the backend

```bash
go mod tidy
go run ./cmd/mailtail
```

The backend creates `data/mailtail.db` automatically.

### Start the frontend

```bash
cd web
npm install
npm run dev
```

For a production-like local run, build the frontend and let the Go server serve the static files:

```bash
cd web
npm install
npm run build
cd ..
go run ./cmd/mailtail
```

## Docker

### Build and run directly

```bash
make docker-run
```

Open the UI at [http://localhost:8080](http://localhost:8080). Send SMTP mail to `localhost:8025`.

Useful container commands:

```bash
make docker-logs
make docker-stop
make docker-rm
```

Data is persisted in the Docker volume `mailtail-data` by default.

### GitHub release workflow

Pushing a Git tag that starts with `v` creates a GitHub Release and publishes a multi-arch image to GHCR.

Example:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Published image:

```text
ghcr.io/vquie/mailtail:v0.1.0
ghcr.io/vquie/mailtail:v0.1
ghcr.io/vquie/mailtail:v0
ghcr.io/vquie/mailtail:0.1.0
ghcr.io/vquie/mailtail:0.1
ghcr.io/vquie/mailtail:0
ghcr.io/vquie/mailtail:latest
```

The workflow uses the repository `GITHUB_TOKEN`, so no extra registry secret is required as long as GitHub Actions has permission to write packages.
The Git tag itself must start with `v`, for example `v0.1.0`.

## REST API

- `GET /api/messages`
- `GET /api/messages?q=invoice`
- `GET /api/messages/{id}`
- `GET /api/messages/{id}/raw`
- `GET /api/messages/{id}/attachments/{attachmentId}`
- `DELETE /api/messages/{id}`
- `DELETE /api/messages`
- `GET /api/stats`

## Example SMTP test

```bash
curl --url smtp://localhost:8025 \
  --mail-from sender@example.test \
  --mail-rcpt receiver@example.test \
  --upload-file sample.eml
```

## Configuration

Environment variables:

- `MAILTAIL_DATA_DIR` default: `data`
- `MAILTAIL_HTTP_ADDR` default: `:8080`
- `MAILTAIL_SMTP_ADDR` default: `:8025`
- `MAILTAIL_WEB_DIR` default: `web/dist`
- `MAILTAIL_AUTH_USERNAME` default: empty, disables login protection for web UI and API and logs a startup warning
- `MAILTAIL_AUTH_PASSWORD` default: empty, disables login protection for web UI and API and logs a startup warning
- `MAILTAIL_ALLOWED_REMOTE_IPS` default: empty, accepts SMTP connections from all IPs and logs a startup warning. Supports IPs and CIDR ranges.
- `MAILTAIL_ACCEPTED_RCPT_DOMAINS` default: empty, accepts recipients for all domains and logs a startup warning. Values may be exact domains or regular expressions.
- `MAILTAIL_ACCEPTED_FROM_DOMAINS` default: empty, accepts senders for all domains and logs a startup warning. Values may be exact domains or regular expressions.

Example:

```bash
cp .env.example .env
make run
```

To enable login protection, set both `MAILTAIL_AUTH_USERNAME` and `MAILTAIL_AUTH_PASSWORD`. If only one is set, MailTail exits on startup.
MailTail then serves a login form and stores an authenticated session in a secure HTTP-only cookie, so you do not need to re-enter credentials on every API request.
This protects the web UI and REST API. SMTP remains unauthenticated in this MVP.
To restrict SMTP access, set `MAILTAIL_ALLOWED_REMOTE_IPS` to a comma-separated list such as `127.0.0.1,10.0.0.0/8,192.168.0.0/16`.
Recipient and sender allow-lists accept either exact domains such as `example.test` or regular expressions such as `^.+@example\\.test$` or `(^|\\.)example\\.test$`.

If a sender domain is not allowed, MailTail rejects `MAIL FROM` with `550 Sender domain not allowed`.
If a recipient domain is not allowed, MailTail rejects `RCPT TO` with `550 Recipient domain not allowed`.

## Future MailFail extension

The SMTP server already exposes a response policy interface:

```go
type SMTPResponsePolicy interface {
    OnConnect(session SessionMetadata) *ResponseError
    OnMailFrom(session SessionMetadata, from string) *ResponseError
    OnRcptTo(session SessionMetadata, recipient string) *ResponseError
    OnData(session SessionMetadata) *ResponseError
}
```

The default policy accepts every command, which keeps the MVP behavior simple while preserving a clean seam for future SMTP failure simulation.

## License

MailTail is licensed under the MIT License. See [LICENSE](/Users/vitaliquiering/git/MailTail/LICENSE).
