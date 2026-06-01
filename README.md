# MailTail

MailTail is a modern open-source SMTP test inbox focused on mail infrastructure testing. This MVP accepts SMTP traffic, stores full RFC822 messages and session metadata, exposes a REST API, and ships with a React-based web UI.

## Features

- SMTP server on port `1025`
- Web UI and REST API on port `8025`
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
make install
make test
make lint
make build
make run
make docker-run
```

`make lint` runs MegaLinter in Docker. `make lint-fix` enables automatic fixes where supported by the active linters.

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

Open the UI at [http://localhost:8025](http://localhost:8025). Send SMTP mail to `localhost:1025`.

Useful container commands:

```bash
make docker-logs
make docker-stop
make docker-rm
```

Data is persisted in the Docker volume `mailtail-data` by default.

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
curl --url smtp://localhost:1025 \
  --mail-from sender@example.test \
  --mail-rcpt receiver@example.test \
  --upload-file sample.eml
```

## Configuration

Environment variables:

- `MAILTAIL_DATA_DIR` default: `data`
- `MAILTAIL_HTTP_ADDR` default: `:8025`
- `MAILTAIL_SMTP_ADDR` default: `:1025`
- `MAILTAIL_WEB_DIR` default: `web/dist`

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
