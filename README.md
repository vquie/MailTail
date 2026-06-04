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
- `GET /api/messages?q=invoice&limit=25`
- `GET /api/messages?q=invoice&cursor=<cursor>`
- `GET /api/messages/{id}`
- `GET /api/messages/{id}/raw`
- `GET /api/messages/{id}/attachments/{attachmentId}`
- `GET /api/settings`
- `PUT /api/settings`
- `DELETE /api/messages/{id}`
- `DELETE /api/messages`
- `GET /api/stats`

### REST API authentication

When `MAILTAIL_ADMIN_USERNAME` and `MAILTAIL_ADMIN_PASSWORD` are set, MailTail protects both the web UI and the REST API with a session-based login flow.

Authentication flow:

1. `GET /login` returns the login form.
2. `POST /auth/login` accepts form-encoded `username` and `password`.
3. On success, MailTail sets:
   - `mailtail_session`: HTTP-only session cookie
   - `mailtail_csrf`: CSRF token cookie used by API clients for mutating requests
4. `POST /auth/logout` clears the session.

Behavior:

- unauthenticated `GET /api/...` requests return `401 {"error":"authentication required"}`
- mutating requests without a valid `X-CSRF-Token` return `403 {"error":"invalid csrf token"}`
- login attempts are rate-limited to 5 failed attempts per 15 minutes per client IP

Example login and API usage with `curl`:

```bash
curl -i -c cookies.txt \
  -X POST http://localhost:8080/auth/login \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data 'username=admin&password=change-me'
```

Read-only request with the stored session:

```bash
curl -b cookies.txt http://localhost:8080/api/messages
```

Mutating request with CSRF token:

```bash
csrf_token="$(awk '$6 == \"mailtail_csrf\" {print $7}' cookies.txt)"

curl -i -b cookies.txt \
  -X DELETE http://localhost:8080/api/messages \
  -H "X-CSRF-Token: ${csrf_token}"
```

Logout:

```bash
csrf_token="$(awk '$6 == \"mailtail_csrf\" {print $7}' cookies.txt)"

curl -i -b cookies.txt \
  -X POST http://localhost:8080/auth/logout \
  -H "X-CSRF-Token: ${csrf_token}"
```

### REST API response examples

`GET /api/messages`

```json
{
  "hasMore": true,
  "nextCursor": "MjAyNi0wNi0wMVQxODowMjo0M1oufDEy",
  "messages": [
    {
      "id": 12,
      "subject": "Test message",
      "mailFrom": "sender@example.test",
      "rcptTo": ["receiver@example.test"],
      "receivedAt": "2026-06-01T18:02:43Z",
      "size": 259
    }
  ]
}
```

Notes:

- search always runs across the full inbox, not just the currently loaded page
- pagination is cursor-based and sorted by `receivedAt DESC, id DESC`
- the default page size is `25`
- `limit` can be overridden per request and is capped server-side

`GET /api/messages/{id}`

```json
{
  "message": {
    "id": 12,
    "subject": "Test message",
    "messageId": "<abc@example.test>",
    "mailFrom": "sender@example.test",
    "rcptTo": ["receiver@example.test"],
    "textBody": "Hello",
    "htmlBody": "<p>Hello</p>",
    "headers": [
      { "key": "Subject", "value": "Test message" }
    ],
    "attachments": [],
    "rawSize": 259
  }
}
```

`GET /api/stats`

```json
{
  "messageCount": 1,
  "totalSize": 259
}
```

`GET /api/settings`

```json
{
  "settings": {
    "allowedOrigins": "https://mailtail.example.com",
    "smtpLogVerbose": false,
    "mailFailEnabled": true,
    "mailFailRulesFile": "examples/mailfail.yaml",
    "allowedRemoteIps": "127.0.0.1,::1",
    "acceptedRcptDomains": "example.test,internal.test",
    "acceptedFromDomains": "sender.test"
  }
}
```

Error format:

```json
{
  "error": "message not found"
}
```

## Example SMTP test

```bash
curl --url smtp://localhost:8025 \
  --mail-from sender@example.test \
  --mail-rcpt receiver@example.test \
  --upload-file sample.eml
```

## MailFail

MailTail can optionally simulate SMTP failures based on the localpart of the sender or recipient address.

MailFail is disabled by default and requires:

- `MAILTAIL_MAILFAIL_ENABLED=true`
- `MAILTAIL_MAILFAIL_RULES_FILE=/path/to/mailfail.yaml`

With the provided Makefile you can keep a local path in `.env`, for example:

```env
MAILTAIL_MAILFAIL_ENABLED=true
MAILTAIL_MAILFAIL_RULES_FILE=examples/mailfail.yaml
```

Then:

```bash
make run
```

or:

```bash
make docker-run
```

`make docker-run` mounts the configured rules file read-only into the container automatically and rewrites the container-side path for MailTail.

Rules are loaded from YAML. See [examples/mailfail.yaml](/Users/vitaliquiering/git/MailTail/examples/mailfail.yaml).

Example:

```yaml
rules:
  - name: user-unknown
    trigger: mf-user-unknown
    stage: rcpt
    action: reject
    code: 550
    enhancedCode: "5.1.1"
    message: "User unknown"

  - name: quota
    trigger: mf-quota
    stage: data
    action: reject
    code: 552
    enhancedCode: "5.2.2"
    message: "Mailbox full"

  - name: greylist
    trigger: mf-greylist
    stage: rcpt
    action: greylist
    allowAfter: 1
    minRetryAfter: 5m
    resetAfter: 1h
    code: 451
    enhancedCode: "4.7.1"
    message: "Try again later"
```

Triggering is done via plus-addressing in the localpart:

- `test+mf-user-unknown@example.test`
- `test+mf-quota@example.test`

Supported stages for localpart-driven rules:

- `mailfrom`
- `rcpt`
- `data`

Supported actions:

- `reject`
  Returns the configured SMTP failure every time.
- `greylist`
  Returns a temporary failure for the first matching attempts and then accepts later retries for the same sender/recipient/trigger combination.
  `allowAfter: 1` means "reject once, accept on the second attempt".
  `minRetryAfter` defines how long the sender must wait before the retry is accepted.
  `resetAfter` defines when the greylist state expires and becomes temporary again. The default is `1h`.

Examples:

- `550 5.1.1 User unknown`
- `451 4.7.1 Try again later`
- `552 5.2.2 Mailbox full`

For greylisting, the state key is based on:

- rule trigger
- stage
- `MAIL FROM`
- matching recipient address

That makes repeated retry tests deterministic for a given sender/recipient pair.
If `minRetryAfter` is set, retries that arrive too early continue to receive the temporary failure until the wait period has passed.

## Configuration

Bootstrap environment variables:

- `MAILTAIL_DATA_DIR` default: `data`
- `MAILTAIL_HTTP_ADDR` default: `:8080`
- `MAILTAIL_SMTP_ADDR` default: `:8025`
- `MAILTAIL_WEB_DIR` default: `web/dist`
- `MAILTAIL_ADMIN_USERNAME` default: empty, disables login protection for web UI and API and logs a startup warning
- `MAILTAIL_ADMIN_PASSWORD` default: empty, disables login protection for web UI and API and logs a startup warning

Runtime settings:

- The web UI includes a Settings panel for MailFail, SMTP logging, origin restrictions, SMTP IP restrictions, sender/recipient restrictions, and automatic message deletion.
- These settings are persisted in SQLite and applied live without restarting MailTail.
- The related environment variables are still supported as bootstrap values for the first start or for instances without saved runtime settings yet.
- Once runtime settings have been saved in the UI, the database values take precedence over the environment.

The runtime-setting bootstrap variables are:

- `MAILTAIL_ALLOWED_ORIGINS` default: empty, disables cross-origin browser access. Set this only if you intentionally need browser clients from another origin.
- `MAILTAIL_SMTP_LOG_VERBOSE` default: `false`, logs only accepted messages and rejects/errors. Set to `true` for per-command SMTP tracing.
- `MAILTAIL_MAILFAIL_ENABLED` default: `false`
- `MAILTAIL_MAILFAIL_RULES_FILE` default: `examples/mailfail.yaml`
- `MAILTAIL_ALLOWED_REMOTE_IPS` default: empty, accepts SMTP connections from all IPs and logs a startup warning. Supports IPs and CIDR ranges.
- `MAILTAIL_ACCEPTED_RCPT_DOMAINS` default: empty, accepts recipients for all domains and logs a startup warning. Values may be exact domains or regular expressions.
- `MAILTAIL_ACCEPTED_FROM_DOMAINS` default: empty, accepts senders for all domains and logs a startup warning. Values may be exact domains or regular expressions.

For the planned multi-user direction and the intended split between instance-wide settings and user-owned mail policies, see [docs/multi-user-target.md](/Users/vitaliquiering/git/MailTail/docs/multi-user-target.md).

Example:

```bash
cp .env.example .env
make run
```

To enable login protection, set both `MAILTAIL_ADMIN_USERNAME` and `MAILTAIL_ADMIN_PASSWORD`. If only one is set, MailTail exits on startup.
MailTail then serves a login form and stores an authenticated session in a secure HTTP-only cookie, so you do not need to re-enter credentials on every API request.
This protects the web UI and REST API. SMTP remains unauthenticated in this MVP.
When MailTail runs behind TLS termination, make sure your proxy forwards `X-Forwarded-Proto: https` or `Forwarded: proto=https` so the session cookie is marked `Secure`.
Cross-origin browser access is off by default. If you explicitly need it, set `MAILTAIL_ALLOWED_ORIGINS` to a comma-separated allow-list such as `https://mail.example.com,https://ops.example.com`.
To restrict SMTP access, set `MAILTAIL_ALLOWED_REMOTE_IPS` to a comma-separated list such as `127.0.0.1,10.0.0.0/8,192.168.0.0/16`.
Recipient and sender allow-lists accept either exact domains such as `example.test` or regular expressions such as `^.+@example\\.test$` or `(^|\\.)example\\.test$`.

If a sender domain is not allowed, MailTail rejects `MAIL FROM` with `550 Sender domain not allowed`.
If a recipient domain is not allowed, MailTail rejects `RCPT TO` with `550 Recipient domain not allowed`.

## MailFail roadmap

MailFail is already available as an initial MVP through the SMTP response policy layer:

```go
type SMTPResponsePolicy interface {
    OnConnect(session SessionMetadata) *ResponseError
    OnMailFrom(session SessionMetadata, from string) *ResponseError
    OnRcptTo(session SessionMetadata, recipient string) *ResponseError
    OnData(session SessionMetadata) *ResponseError
}
```

The current MailFail implementation supports:

- localpart-based triggers via plus-addressing
- configurable SMTP replies from YAML
- `reject`
- `greylist`

Planned next steps include:

- `delay`
- `disconnect`
- artificial timeouts
- probabilistic failures
- more advanced matching beyond the localpart trigger

## License

MailTail is licensed under the MIT License. See [LICENSE](/Users/vitaliquiering/git/MailTail/LICENSE).
