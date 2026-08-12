# Outbound reports

MailTail rules can accept an inbound test message and asynchronously send a report to its SMTP envelope sender. The generated message is stored in a durable SQLite outbox before delivery. Delivery failures remain queued without a fixed attempt limit. Temporary failures are retried with exponential backoff capped at one hour.

## Actions

All outbound actions run after MailTail has accepted `DATA` and match the same plus-address trigger used by other MailFail rules. Their SMTP stage is implicit rather than configurable. MailTail generates standard human-readable text for feedback reports, while asynchronous bounces may override their diagnostic text per rule.

- `arf`: RFC 5965 `multipart/report` with a registered feedback type and the original message. It uses the configured or derived report sender.
- `xarf-v3`: Legacy XARF v3 spam JSON transported in an ARF-shaped feedback report. It uses the configured or derived report sender.
- `xarf-v4`: XARF v4.2 `messaging/spam` JSON with SHA-256 evidence metadata. It uses the configured or derived report sender.
- `original-report`: RFC 5965 `other` feedback report with the original RFC822 message. It uses the configured or derived report sender.
- `async-bounce`: RFC 3464 `multipart/report; report-type=delivery-status` with an empty reverse path (`<>`).

Report messages include `Auto-Submitted: auto-generated` and `X-Auto-Response-Suppress: All`. MailTail does not create reports for messages with an empty envelope sender or an existing non-`no` `Auto-Submitted` header.

ARF rules default to `Feedback-Type: abuse`. The Rules UI also exposes the registered ARF feedback types `fraud`, `virus`, `other`, and `not-spam`.

ARF, XARF v3, and XARF v4 rules may set an optional report recipient local part. MailTail retains the domain of the original SMTP envelope sender and replaces only its local part. For example, `reportRecipientLocalPart: fbl` sends a report for `bounce-123@example.test` to `fbl@example.test`.

The value is limited to a 64-byte ASCII dot-atom local part; addresses containing `@`, domains, whitespace, quoted strings, leading or trailing dots, and consecutive dots are rejected. Leaving it empty preserves delivery to the complete original envelope sender. Original-message reports and asynchronous bounces do not support this override.

XARF transport uses `Feedback-Type: xarf`, which is an intentional unofficial RFC 5965 extension defined by XARF. XARF itself is not an IETF RFC. Its third MIME part is the XARF JSON document. MailTail's v3 and v4 payloads are tested against pinned copies of the official spam schemas.

XARF reports include the actual source IP and source TCP port of the inbound SMTP connection. MailTail refuses to generate XARF when either value is unavailable or invalid rather than substituting misleading transport evidence.

An `original-report` uses `Feedback-Type: other` so ARF parsers can discover its `message/feedback-report` metadata and attached `message/rfc822` part.

For `async-bounce`, the optional rule text is written to both the first human-readable MIME part and the SMTP `Diagnostic-Code` in `message/delivery-status`. If it is empty, MailTail uses its standard delivery failure text. Because the registered `smtp` diagnostic type uses graphic US-ASCII, custom diagnostic text must contain printable US-ASCII and is limited to 700 characters.

## Conformance guarantees

The report generator and its tests enforce the following contracts:

- ARF and original-message feedback use the RFC 5965 three-part `multipart/report` layout. The machine-readable part contains exactly one `Feedback-Type`, `User-Agent`, and `Version` field and terminates as a complete RFC-style field block.
- Asynchronous bounces use the RFC 3464 `multipart/report; report-type=delivery-status` layout, an empty envelope reverse path, one per-message field block, and one complete per-recipient field block.
- XARF v3 and v4 JSON payloads validate with format assertions against pinned official schemas. The containing email follows the XARF email transport layout.
- Generated MIME is parsed again in tests with strict field-block and multipart readers. The tests specifically cover the byte-level blank-line terminators required by Halon's `MailMessage::String()` flow.
- Non-ASCII human-readable report text is quoted-printable. If an attached original message requires 8-bit transport, delivery is attempted only when the receiving SMTP server advertises `8BITMIME`.

The schemas are pinned under `internal/smtpserver/testdata/xarf` with their upstream commit IDs so schema changes cannot silently alter the acceptance criteria.

## Report sender

The report sender is a mailbox-level setting in the Rules UI.

1. When `Report sender` contains an email address, MailTail uses that address for the visible `From` header and for non-DSN report envelopes.
2. When it is empty and the matching recipient uses an exact configured accepted domain, MailTail derives `postmaster@<matching-domain>`.
3. When recipient ownership is defined by a regex or is unrestricted, an explicit report sender is required.

The original recipient address is never reused as the report sender. Recipient local parts and plus tags are controlled by the SMTP client, so copying them would create arbitrary sender identities and increase loop and spoofing risk.

Asynchronous bounces still use the configured or derived address in the visible `From` header, but their SMTP envelope sender is always empty as required for safe DSN behavior.

## Outbound transport

Outbound delivery has one instance-wide mode that applies to ARF, XARF, original-message reports, and asynchronous bounces alike. `direct` is the default. Set `MAILTAIL_OUTBOUND_MODE=relay` only when every outbound action should use a relay.

SMTP `4xx` responses, network timeouts, and temporary DNS failures retain the report in the SQLite outbox. Once a destination returns a temporary failure, other due reports for the same recipient domain are moved to the same persistent retry time instead of being attempted immediately.

This domain backoff survives process restarts because every affected outbox entry receives its own updated `next_attempt` value.

### Direct-to-MX delivery

```env
MAILTAIL_OUTBOUND_MODE=direct
MAILTAIL_OUTBOUND_SMTP_HELO=mailtail.example.test
```

Direct mode resolves the recipient domain's MX records, tries them in priority order, and falls back to the domain's A/AAAA records when no MX record exists. A null MX is treated as an explicit refusal of email.

Delivery uses SMTP on port 25 and upgrades with STARTTLS when the destination advertises it. STARTTLS is opportunistic in direct mode: MailTail keeps the transport encrypted but does not reject an MX because its certificate is expired, self-signed, or valid for a different hostname.

For reliable internet delivery, the configured HELO hostname should resolve to the sending IP and match its PTR. The report sender domain should authorize that IP through SPF and should use DKIM/DMARC where required. The host or network must permit outbound TCP port 25.

### Relay delivery

Relay credentials are intentionally not returned by the API or stored in mailbox settings.

```env
MAILTAIL_OUTBOUND_MODE=relay
MAILTAIL_OUTBOUND_SMTP_ADDR=relay.example.test:587
MAILTAIL_OUTBOUND_SMTP_TLS=starttls
MAILTAIL_OUTBOUND_SMTP_USERNAME=mailtail
MAILTAIL_OUTBOUND_SMTP_PASSWORD=change-me
MAILTAIL_OUTBOUND_SMTP_HELO=mailtail.example.test
```

`MAILTAIL_OUTBOUND_SMTP_TLS` accepts:

- `starttls`: require the relay to advertise STARTTLS before authentication or delivery.
- `tls`: connect using implicit TLS.
- `none`: use plain SMTP. This is intended only for trusted local test relays.

Relay TLS validates the configured relay's certificate. This remains strict because relay mode can transmit SMTP credentials.

`MAILTAIL_OUTBOUND_SMTP_ADDR` is required in relay mode. MailTail exits during startup instead of silently queuing undeliverable reports when the relay address is missing.

Outbound rules can intentionally send mail to any accepted message's envelope sender. Before enabling them outside an isolated test network, restrict inbound SMTP with the mailbox `Allowed remote IPs` setting and avoid exposing MailTail as a public report generator.
