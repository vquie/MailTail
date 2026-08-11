# Outbound reports

MailTail rules can accept an inbound test message and asynchronously send a report to its SMTP envelope sender. The generated message is stored in a durable SQLite outbox before delivery. Temporary delivery failures are retried with exponential backoff capped at one hour.

## Actions

All outbound actions run after MailTail has accepted `DATA` and match the same plus-address trigger used by other MailFail rules. Their SMTP stage is implicit rather than configurable, and MailTail generates the standard human-readable report text.

| Action            | Generated format                                                                    | SMTP envelope sender                |
|-------------------|-------------------------------------------------------------------------------------|-------------------------------------|
| `arf`             | RFC 5965 `multipart/report` with `message/feedback-report` and the original message | Configured or derived report sender |
| `xarf-v3`         | Legacy XARF v3 JSON transported in a feedback report                                | Configured or derived report sender |
| `xarf-v4`         | XARF v4 `messaging/spam` JSON with SHA-256 evidence metadata                        | Configured or derived report sender |
| `original-report` | `multipart/mixed` with the original RFC822 message                                  | Configured or derived report sender |
| `async-bounce`    | RFC 3464 `multipart/report; report-type=delivery-status`                            | Empty reverse path (`<>`)           |

Report messages include `Auto-Submitted: auto-generated` and `X-Auto-Response-Suppress: All`. MailTail does not create reports for messages with an empty envelope sender or an existing non-`no` `Auto-Submitted` header.

## Report sender

The report sender is a mailbox-level setting in the Rules UI.

1. When `Report sender` contains an email address, MailTail uses that address for the visible `From` header and for non-DSN report envelopes.
2. When it is empty and the matching recipient uses an exact configured accepted domain, MailTail derives `postmaster@<matching-domain>`.
3. When recipient ownership is defined by a regex or is unrestricted, an explicit report sender is required.

The original recipient address is never reused as the report sender. Recipient local parts and plus tags are controlled by the SMTP client, so copying them would create arbitrary sender identities and increase loop and spoofing risk.

Asynchronous bounces still use the configured or derived address in the visible `From` header, but their SMTP envelope sender is always empty as required for safe DSN behavior.

## Outbound transport

Outbound delivery has one instance-wide mode that applies to ARF, XARF, original-message reports, and asynchronous bounces alike. `direct` is the default. Set `MAILTAIL_OUTBOUND_MODE=relay` only when every outbound action should use a relay.

### Direct-to-MX delivery

```env
MAILTAIL_OUTBOUND_MODE=direct
MAILTAIL_OUTBOUND_SMTP_HELO=mailtail.example.test
```

Direct mode resolves the recipient domain's MX records, tries them in priority order, and falls back to the domain's A/AAAA records when no MX record exists. A null MX is treated as an explicit refusal of email. Delivery uses SMTP on port 25 and upgrades with STARTTLS when the destination advertises it.

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

`MAILTAIL_OUTBOUND_SMTP_ADDR` is required in relay mode. MailTail exits during startup instead of silently queuing undeliverable reports when the relay address is missing.

Outbound rules can intentionally send mail to any accepted message's envelope sender. Before enabling them outside an isolated test network, restrict inbound SMTP with the mailbox `Allowed remote IPs` setting and avoid exposing MailTail as a public report generator.
