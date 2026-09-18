# Multi-User Target

This document describes MailTail's current multi-user foundation and the intended direction for future tenant features.

The current implementation supports local users, user-owned settings and messages, recipient-domain routing, separate MailFail policies, owner-scoped API access, and an environment-admin mailbox. A mailbox without an accepted recipient domain is intentionally not routable. SMTP envelopes may contain multiple recipients only when every accepted recipient resolves to the same owner.

The profile, external identity provider, quota, and richer tenant concepts below remain future work.

## Goals

- multiple users can log into one MailTail instance
- each user can have different mail acceptance and MailFail behavior
- settings become user-scoped by default
- global instance settings stay separate from user policy settings
- message data can be isolated per user or tenant

## Split The Current Settings Model

The current runtime settings are a useful bridge, but they should eventually be split into two categories.

### Global system settings

These belong to the running instance itself and should not vary per user:

- HTTP listen address
- SMTP listen address
- web asset path
- auth mode and auth backend
- instance-wide CORS defaults, if any
- storage backend selection
- logging sinks and process-level logging defaults

### User policy settings

These should become user-owned:

- accepted recipient domains
- accepted sender domains
- allowed remote IPs
- MailFail enabled
- MailFail rules
- SMTP logging verbosity, if this is meant to reflect a user workflow rather than an operator workflow

## Recommended Data Model

### users

Core identity records currently include:

- `id`
- `username`
- `password_hash`
- `created_at`
- `updated_at`

Display names and explicit active/disabled state remain future additions.

### user_settings

One row per user currently stores the mailbox settings JSON, keyed by:

- `user_id`
- `updated_at`

### user_mail_policies

Mailbox policy fields currently live in the user's settings JSON. A normalized policy table remains a future option.

Suggested fields:

- `id`
- `user_id`
- `accepted_rcpt_domains`
- `accepted_from_domains`
- `allowed_remote_ips`
- `mailfail_enabled`
- `mailfail_rules_json` or `mailfail_rules_yaml`
- `created_at`
- `updated_at`

If the product later supports multiple inbound identities per user, this should likely become profile-based instead:

- `mail_profiles`
- `mail_profile_policies`

### messages

Messages are owned explicitly through:

- `owner_user_id`
- optional `mail_profile_id`
- `expires_at`

That allows:

- user-scoped inboxes
- access control in the UI and API
- future filtering and quotas
- retention cleanup without per-user workers

### auth_sessions

Already persisted in SQLite with:

- `session_id`
- `user_id`
- `username`
- `is_admin`
- `csrf_token`
- `expires_at`

### auth_login_attempts

Already persisted in SQLite. In a multi-user setup this can stay mostly unchanged, though it may later be useful to include:

- `attempted_username`
- `user_id` when known

### greylist_states

Already persisted in SQLite. This should eventually be partitioned by policy owner.

Suggested target key material:

- `user_id`
- `mail_profile_id` if profiles exist
- `stage`
- `trigger`
- `mail_from`
- `rcpt_to`

## SMTP Routing Requirement

The routing decision is:

How does MailTail decide which user's policy applies to an incoming SMTP session?

MailTail currently answers it using recipient-domain ownership before user-specific policy checks run.

### Preferred routing signals

The cleanest options are:

1. recipient domain
2. explicit mail profile / inbox identity
3. dedicated SMTP hostname or listener per tenant

The weakest option is trying to infer ownership too late from message content.

### Current strategy

Use recipient domain ownership as the first routing key.

Example:

- user A owns `inbox-a.example.test`
- user B owns `inbox-b.example.test`

Then MailTail can:

1. inspect `RCPT TO`
2. resolve domain ownership
3. load the matching user policy
4. evaluate accept/reject/MailFail rules against that policy

Mailboxes without a recipient domain are excluded from routing. Ambiguous ownership and mixed-owner SMTP envelopes are rejected.

## API Direction

The current API uses `GET/PUT /api/settings` for the signed-in user's settings and separate `/api/admin/...` endpoints for admin mailbox and user management. A more explicit future shape could be:

Recommended target API shape:

- `GET /api/me`
- `GET /api/me/settings`
- `PUT /api/me/settings`
- `GET /api/me/policies`
- `PUT /api/me/policies/{id}`

For admin workflows later:

- `GET /api/admin/users`
- `GET /api/admin/users/{id}/policies`
- `PUT /api/admin/users/{id}/policies`

## UI Direction

The Settings panel is user-scoped by default:

- a user edits their own policy settings
- admins can switch context to manage another user
- the inbox view only shows messages owned by the current user or tenant

## Retention And Cleanup

Automatic message deletion should not evolve into one background worker per user.

The intended direction is:

- a single cleanup worker per MailTail process
- messages carry their own computed retention timestamp, e.g. `expires_at`
- retention is derived from the owning user or mail profile policy at ingest time
- cleanup deletes expired messages in small batches

Recommended shape:

- add `expires_at` to `messages`
- index `expires_at`
- run one central cleanup worker for all users
- delete with batched queries, for example `LIMIT 500` per pass

That avoids:

- scanning all users every cycle
- spawning per-user workers
- large delete spikes on busy systems

This should become the preferred model once retention is fully user-scoped.

## Current Compatibility Guidance

When extending the current multi-user implementation:

- prefer storing state in the database rather than only in memory
- avoid hard-coding the assumption that one instance has exactly one policy
- avoid naming things as `global settings` unless they truly are instance-wide
- keep SMTP policy evaluation capable of loading settings from an owner-specific context

## Near-Term Evolution

The next useful extensions are:

1. split the current persisted settings document into explicit instance and mailbox-policy records
2. introduce mail profiles when one user needs multiple independently managed inbound identities
3. add external authentication providers without changing message ownership semantics
4. add quotas and retention policy reporting per mailbox
