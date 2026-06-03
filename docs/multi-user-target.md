# Multi-User Target

This document describes the intended direction for MailTail once the project moves from a single-admin instance to a real multi-user setup.

It is not implemented yet. It exists to keep current changes compatible with that future.

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

Core identity records.

Suggested fields:

- `id`
- `email` or `username`
- `password_hash`
- `display_name`
- `is_active`
- `created_at`
- `updated_at`

### user_settings

One row per user for general UI/runtime preferences.

Suggested fields:

- `user_id`
- `smtp_log_verbose`
- `created_at`
- `updated_at`

### user_mail_policies

One row per user, or per user profile if multiple profiles per user are needed later.

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

Messages should eventually be owned explicitly.

Suggested additions:

- `owner_user_id`
- optional `mail_profile_id`

That allows:

- user-scoped inboxes
- access control in the UI and API
- future filtering and quotas

### auth_sessions

Already persisted in SQLite. This table is compatible with multi-user and should evolve to reference a real user row instead of only storing a username string.

Suggested target fields:

- `session_id`
- `user_id`
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

The critical future design question is:

How does MailTail decide which user's policy applies to an incoming SMTP session?

That decision must happen before policy checks can be user-specific.

### Preferred routing signals

The cleanest options are:

1. recipient domain
2. explicit mail profile / inbox identity
3. dedicated SMTP hostname or listener per tenant

The weakest option is trying to infer ownership too late from message content.

### Recommended first strategy

Use recipient domain ownership as the first routing key.

Example:

- user A owns `inbox-a.example.test`
- user B owns `inbox-b.example.test`

Then MailTail can:

1. inspect `RCPT TO`
2. resolve domain ownership
3. load the matching user policy
4. evaluate accept/reject/MailFail rules against that policy

This is the simplest path toward multi-user without redesigning SMTP flow later.

## API Direction

The current global settings API should be treated as transitional.

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

The current Settings panel should eventually become user-scoped by default.

That means:

- a user edits their own policy settings
- admins may optionally switch context to manage another user
- the inbox view only shows messages owned by the current user or tenant

## Current Compatibility Guidance

When changing the current codebase before multi-user is implemented:

- prefer storing state in the database rather than only in memory
- avoid hard-coding the assumption that one instance has exactly one policy
- avoid naming things as `global settings` unless they truly are instance-wide
- keep SMTP policy evaluation capable of loading settings from an owner-specific context

## Near-Term Refactor Recommendation

Before implementing real multi-user auth, the next clean preparatory step would be:

1. rename current persisted runtime settings to clarify that they are instance-scoped
2. introduce a separate concept for mail policy settings
3. make SMTP policy creation consume a policy object rather than a flat app settings object

That keeps the future migration from:

- one instance policy

to:

- many user-owned policies

much more direct.
