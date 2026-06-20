# Upstream Source Channel Sync Design

## Goal

Add an admin-managed upstream source system that can connect to a sub2api upstream, discover its available upstream groups and multipliers, create one local Wynth API channel per selected upstream group, and keep those local channels mapped to the upstream source.

This is the first source automation step. It must store price/multiplier metadata for later scheduling work, but it must not automatically change local channel priority or weight yet.

## Product Decisions

- Implement sub2api source support first.
- Treat new-api source support as a later adapter using the same source and mapping core.
- Create local channels from upstream groups, not from manual browser automation.
- Keep local channel priority and weight user-configured in this step.
- Store upstream group multiplier metadata now so the next feature can convert price to priority.
- Do not migrate account-pool logic in this step.
- Do not delete local channels automatically when an upstream group disappears or is unselected.
- Do not automatically recreate a local channel after an admin deletes it; mark the mapping as needing attention.
- Do not store test credentials, example passwords, or real upstream secrets in docs, tests, fixtures, logs, or audit messages.

## Current Behavior

Wynth API can already manage local channels, update channel model lists from an upstream `/v1/models` endpoint, monitor selected local channels, and route by strict priority.

There is no first-class record for an upstream account/source. Admins must manually create one local channel per upstream group or pricing tier, manually create the upstream API keys, and manually copy the upstream key into the local channel. The system therefore cannot reliably know which local channels came from the same upstream, which upstream multiplier each channel represents, or which channels should participate in future price-based scheduling.

## Target Behavior

An admin can create an upstream source with:

- source name;
- source type, initially `sub2api`;
- source origin URL;
- optional admin API base path, default `/api/v1`;
- relay base URL override, default source origin URL;
- sub2api login credentials;
- default local channel settings for generated channels;
- selected upstream groups to sync.

The admin can then run discovery. Discovery logs in to the upstream source, fetches available groups and group rates, normalizes them into local mapping rows, and displays a preview table.

The admin selects upstream groups and runs sync. Sync creates or updates one local channel for each selected upstream group. Each generated local channel is linked to a source mapping row. The mapping row stores the upstream group id, upstream group name, upstream key id, local channel id, and effective upstream multiplier.

## Scope

In scope:

- New upstream source model and migrations.
- New upstream source to local channel mapping model and migrations.
- sub2api client adapter for login, group discovery, rate discovery, key creation, and key update where supported.
- Admin APIs for source CRUD, discovery preview, selected group sync, and sync result inspection.
- Minimal admin UI for managing sources and syncing selected sub2api groups.
- Local channel creation/update from selected upstream groups.
- Audit logs for source creation/update, discovery, sync, and secret changes.
- Tests for source validation, sub2api response normalization, mapping persistence, idempotent sync, secret redaction, and generated channel fields.

Out of scope:

- Automatic priority or weight adjustment from price.
- Cache-rate scoring.
- Availability-based priority scoring.
- new-api upstream adapter implementation.
- Account-pool migration.
- Web browser automation against upstream dashboards.
- Automatic destructive cleanup of upstream keys or local channels.
- A global monitor switch.

## Data Model

Add `model.UpstreamSource`.

Fields:

- `Id int`
- `Name string`
- `Type string`
- `Status int`
- `BaseURL string`
- `AdminAPIBasePath string`
- `RelayBaseURL string`
- `AuthConfig string`
- `SyncConfig string`
- `CurrentSyncToken string`
- `SyncStartedAt int64`
- `LastDiscoveryTime int64`
- `LastDiscoveryStatus string`
- `LastDiscoveryError string`
- `LastSyncTime int64`
- `LastSyncStatus string`
- `LastSyncError string`
- `CreatedTime int64`
- `UpdatedTime int64`

Store `AuthConfig` and `SyncConfig` as `TEXT` JSON strings for SQLite, MySQL, and PostgreSQL compatibility. Do not use database-native JSON column types.

`AuthConfig` must be tagged `json:"-"` on the GORM model so returning the model by mistake cannot expose credentials. Normal handlers should still use response DTOs instead of raw model structs.

Use explicit string column sizes for indexed string fields, especially on MySQL. Any indexed string column should use a bounded type such as `varchar(191)` or a project-appropriate shorter size.

Source status values:

```text
enabled
disabled
deleted
```

The first implementation should use status-based soft deletion. Queries should exclude `deleted` by default. Do not add `gorm.DeletedAt` unless the implementation plan also handles uniqueness, filters, and restore behavior.

Initial `AuthConfig` shape for sub2api:

```text
{
  "auth_type": "password",
  "email": "...",
  "password": "...",
  "access_token": "...",
  "refresh_token": "...",
  "expires_at": 0
}
```

If sub2api returns refresh tokens or reusable access tokens, persist those tokens and reuse them before falling back to password login. If the upstream requires password persistence, the first implementation may follow the existing channel-key plaintext-at-rest model, but the UI and docs must explicitly state that stored upstream credentials are plaintext in the database unless a later encryption feature is added.

Initial `SyncConfig` shape:

```text
{
  "local_group": "default",
  "channel_type": 1,
  "default_priority": 0,
  "default_weight": 0,
  "enable_monitor": false,
  "monitor_interval_minutes": 60,
  "auto_sync_models": true
}
```

Add `model.UpstreamSourceChannelMapping`.

Fields:

- `Id int`
- `SourceID int`
- `SyncEnabled bool`
- `UpstreamGroupID string`
- `UpstreamGroupName string`
- `UpstreamPlatform string`
- `DiscoveryStatus string`
- `UpstreamStatus string`
- `UpstreamRateMultiplier *float64`
- `EffectiveRateMultiplier *float64`
- `UpstreamKeyID string`
- `LocalChannelID int`
- `SyncStatus string`
- `LastError string`
- `LastDiscoveredAt int64`
- `LastSyncedAt int64`
- `CreatedTime int64`
- `UpdatedTime int64`

Add a composite unique index on `(source_id, upstream_group_id)`. Add indexes for `source_id`, `local_channel_id`, and `sync_enabled`.

The mapping table is the canonical place for upstream pricing metadata. Do not store these values in `Channel.OtherSettings` unless the frontend needs a derived read-only display field.

Use pointer multipliers so `nil` means unknown and `0.0` remains a valid explicit multiplier.

Mapping discovery status values:

```text
active
stale
invalid
```

Mapping sync status values:

```text
never_synced
synced
failed
skipped
needs_attention
```

## DTO and Secret Handling

Normal source list/detail responses must not return `AuthConfig` or the raw upstream password.

Use separate request DTOs for:

- creating a source with credentials;
- updating non-secret source settings;
- rotating credentials.

Responses may include:

- masked email;
- `has_credentials`;
- source status;
- last discovery status and error;
- last sync status and error;
- discovery/mapping summaries.

Do not log the password, upstream access token, upstream API key, generated local channel key, or raw `AuthConfig`.

## sub2api Adapter

The sub2api adapter should use HTTP APIs, not browser automation.

Default endpoints, relative to the configured admin API base:

- `POST /auth/login`
- `GET /groups/available`
- `GET /groups/rates`
- `GET /keys`
- `POST /keys`
- `PUT /keys/:id`

sub2api wraps successful API responses as:

```text
{
  "code": 0,
  "message": "success",
  "data": ...
}
```

The adapter must unwrap this envelope and treat non-zero `code` values as upstream API errors. Paginated endpoints, including `GET /keys`, return pagination data under `data.items`.

`POST /auth/login` can return either bearer tokens or a 2FA challenge. The first implementation does not support completing sub2api 2FA; if login returns `requires_2fa`, mark the source discovery/sync as failed with a sanitized "2FA required" error.

The adapter should normalize responses into source-agnostic structs:

```text
UpstreamGroup {
  ID string
  Name string
  Platform string
  Status string
  RateMultiplier *float64
  EffectiveRateMultiplier *float64
}

UpstreamKey {
  ID string
  Key string
  Name string
  GroupID string
}
```

sub2api `id` and `group_id` fields are numeric or null in JSON. Normalize them to strings at the adapter boundary so the rest of Wynth API does not depend on sub2api's numeric ids.

`GET /groups/rates` returns a map from group id to user-specific multiplier. It does not return group records.

`POST /keys` returns the full generated key and key id. `PUT /keys/:id` updates key metadata such as name, group, status, quota, and rate limits; it should not be treated as a way to rotate the key string.

Effective multiplier resolution:

1. If `/groups/rates` contains a user-specific value for the group, use it.
2. Otherwise use the group's `rate_multiplier`.
3. If neither value is present, store a nil effective multiplier, allow discovery, and block sync for that group until the UI shows the missing value clearly or the user overrides it later.

HTTP behavior:

- Use bounded request timeouts.
- Trim trailing slashes from the origin URL.
- Accept only `http` and `https` URL schemes.
- Do not follow arbitrary non-HTTP schemes.
- Disable redirects or reject redirects to a different host.
- Reject empty hosts, link-local metadata hosts, and non-routable hosts by default.
- Private network hosts should require an explicit server setting or environment opt-in so self-hosted deployments can still integrate private upstreams intentionally.
- Limit response body sizes before decoding.
- Decode JSON through `common.DecodeJson` or `common.Unmarshal`.

Use the existing SSRF protection and HTTP client facilities instead of creating a parallel validator. The implementation should route sub2api outbound requests through the project's SSRF-aware validation/client path, including `common.ValidateURLWithFetchSetting` and the existing fetch settings such as `AllowPrivateIp` where applicable. Redirect targets must be validated per hop.

Before implementation, pin the sub2api adapter contract against the actual sub2api code or a redacted live capture. The adapter should not rely on unverified endpoint response shapes.

## Discovery Flow

Discovery is read-only against local channels.

Steps:

1. Validate the source configuration.
2. Login to the upstream.
3. Fetch available groups.
4. Fetch user-specific group rates.
5. Normalize group metadata and effective multipliers.
6. Upsert mapping rows by `(source_id, upstream_group_id)`.
7. Mark mappings not returned by discovery with `DiscoveryStatus=stale`, but do not delete them.
8. Record `LastDiscoveredAt` on mappings and `LastDiscoveryTime`, `LastDiscoveryStatus`, and `LastDiscoveryError` on the source.
9. Write an admin audit entry without secrets.

Discovery failure must not modify existing local channels.

## Sync Flow

Sync applies selected mappings to local channels.

For each mapping with `SyncEnabled=true`:

1. Ensure the mapping has a valid effective multiplier.
2. Ensure or create an upstream API key for the upstream group.
3. Ensure or create a local channel linked to the mapping.
4. Update local channel fields owned by the source sync.
5. Populate models before enabling a generated local channel.
6. Refresh channel abilities after channel creation/update.
7. Refresh channel cache and proxy cache after the sync batch.
8. Store per-mapping success or failure status.

Sync should be idempotent. Re-running sync with the same source and selected groups should update existing upstream keys and local channels instead of creating duplicates.

Sync must use a database-backed guarded source state transition so concurrent sync runs cannot create duplicate upstream keys or local channels in multi-node deployments.

The first implementation should use `CurrentSyncToken` and `SyncStartedAt` on `UpstreamSource`. A sync run atomically claims a source only when no token is present or the previous token has expired. The update must check affected rows; if no row is claimed, return a "sync already running" response. Release the token when the run finishes.

An in-process mutex is not sufficient because admin APIs can run on more than one node.

## Mapping Field Ownership

Discovery and sync both write mapping rows, so mapping columns need the same explicit ownership discipline as generated channel columns.

Discovery-owned columns:

- `UpstreamGroupName`
- `UpstreamPlatform`
- `UpstreamStatus`
- `DiscoveryStatus`
- `UpstreamRateMultiplier`
- `EffectiveRateMultiplier`
- `LastDiscoveredAt`

Operator-owned columns:

- `SyncEnabled`

Sync-owned columns:

- `UpstreamKeyID`
- `LocalChannelID`
- `SyncStatus`
- `LastError`
- `LastSyncedAt`

Discovery upsert must update only discovery-owned columns. It must not reset `SyncEnabled`, detach `LocalChannelID`, clear `UpstreamKeyID`, overwrite `SyncStatus`, or clear sync errors.

When discovery marks a missing group stale, it should update only `DiscoveryStatus`, `LastDiscoveredAt`, and source discovery status fields.

## Upstream Key Lifecycle

The first implementation should persist the upstream key id returned by the sub2api `POST /keys` response.

If a mapping has no `UpstreamKeyID`, create a new upstream key. Use a deterministic key name, such as:

```text
Wynth API / <source name> / <upstream group name>
```

If a mapping has `UpstreamKeyID`, update that key when the adapter supports update. If the upstream returns not found, create a replacement key and update the mapping.

If local channel creation fails after upstream key creation, keep the upstream key and record a mapping error. Do not attempt automatic upstream key deletion in the first version.

## Generated Local Channel Rules

For sub2api sources, generated channels should default to OpenAI-compatible channel type (`constant.ChannelTypeOpenAI`) unless the source configuration explicitly chooses another supported local channel type.

Channel field ownership:

- `Name`: source-owned by default, `"<source name> / <upstream group name>"`.
- `Type`: source-owned from sync config.
- `BaseURL`: source-owned from `RelayBaseURL`, default source origin URL.
- `Key`: source-owned from the upstream generated API key.
- `Group`: source-owned from sync config local group.
- `Priority`: source-owned only as the configured default value; no price adjustment yet.
- `Weight`: source-owned only as the configured default value; no price adjustment yet.
- `Tag`: source-owned optional tag, default source name.
- `Models`: source-owned when sync can fetch upstream models or the source provides models in a supported shape.
- `OtherSettings`: may set monitor defaults when creating the channel.

Do not overwrite unrelated local channel fields unless the source sync owns them.

Implementation should update generated channels with an explicit owned-column map or `Select` list. Do not reuse a broad struct update that can clobber unowned fields or skip intended zero values.

`RelayBaseURL` must be non-empty. A blank `BaseURL` falls back to the provider default for the channel type, which would route generated OpenAI-compatible channels to the real OpenAI base URL instead of the sub2api upstream.

Generated channels must not be enabled with an empty model list. The sync implementation should either:

1. fetch models from the upstream using the generated key and set `Models`; or
2. create/update the local channel as manually disabled with a clear mapping error until models are provided.

If `auto_sync_models` is disabled, sync should use option 2 unless the admin supplies an explicit model list in a later feature.

Do not insert an empty-model ability row. The implementation must either add an empty-model guard to the existing ability update helpers or use a source-sync channel create/update path that bypasses ability creation when `Models` is empty. A disabled channel with empty `Models` must also avoid an empty ability row.

Generated channels should include a read-only link back to the mapping through `LocalChannelID`. Avoid placing source metadata in channel names as the only durable link.

## Upstream Model Fetching

Generated channels should be populated with upstream model ids before they are enabled.

The current model-fetch logic lives in controller code. The implementation should extract the reusable pieces into a service-level helper that can be called by:

- existing channel upstream model update endpoints; and
- the new upstream source sync service.

Do not make the new sync service call controller functions directly. Preserve the Router -> Controller -> Service -> Model layering.

## API Surface

Add admin-only routes under the existing API router.

Suggested endpoints:

```text
GET    /api/upstream_sources
POST   /api/upstream_sources
GET    /api/upstream_sources/:id
PUT    /api/upstream_sources/:id
PUT    /api/upstream_sources/:id/credentials
DELETE /api/upstream_sources/:id
POST   /api/upstream_sources/:id/discover
GET    /api/upstream_sources/:id/mappings
PUT    /api/upstream_sources/:id/mappings
POST   /api/upstream_sources/:id/sync
GET    /api/upstream_sources/:id/sync_result
```

Deletion should be conservative:

- Deleting a source must not delete local channels by default.
- The source should be status-marked as `deleted`.
- Hard deletion, upstream key deletion, and local channel deletion can be explicit future actions.

## Frontend

Add a minimal admin surface for upstream sources.

Views:

- source list;
- source create/edit drawer;
- discovery preview and mapping table;
- selected group sync action;
- sync result details.

The mapping table should show:

- upstream group name;
- platform;
- upstream status;
- effective multiplier;
- sync enabled toggle;
- linked local channel id/name;
- upstream key status;
- last discovered time;
- last sync status/error.

Keep the first UI functional and compact. Do not redesign the broader channel page in this step.

## Scheduling

The first version should support manual discovery and manual sync only.

The schema should keep timestamps and status fields that make scheduled sync possible later, but this step should not introduce background source sync. Manual-only behavior reduces the chance of unexpected upstream key creation or local channel changes while the data model is new.

## Security and Audit

- Source management routes must be admin-only.
- Credential update must be a separate explicit operation.
- Normal responses must not expose upstream passwords, access tokens, upstream API keys, or local channel keys.
- Logs and audit entries must contain source id/name and counts, not secrets.
- HTTP client errors should be sanitized before storing `LastDiscoveryError`, `LastSyncError`, or mapping `LastError`.
- Do not store raw upstream response bodies in persistent error fields.
- Strip authorization headers, cookies, query tokens, access tokens, refresh tokens, generated keys, and password-like values from stored errors.
- Cap stored error length.
- URL validation must reject empty hosts, non-HTTP schemes, and blocked internal targets unless explicitly allowed by server configuration.
- Real credentials must not appear in docs, tests, snapshots, or committed fixtures.

Audit actions should cover:

- source created;
- source updated;
- source credentials updated;
- source discovery run;
- mapping selection updated;
- source sync run;
- generated local channel created;
- generated local channel updated.

## Error Handling

Discovery:

- Login failure records source-level failure and does not touch mappings.
- Group fetch failure records source-level failure and does not touch mappings.
- Partial malformed group records are skipped with a warning count.

Sync:

- One group failure must not abort all selected groups.
- Mapping-level failures must be visible in the sync result.
- Existing local channels must not be disabled because upstream sync failed.
- Missing upstream key id should trigger key creation.
- Missing local channel id should trigger local channel creation.
- Missing local channel row for an existing id should mark the mapping `needs_attention` by default. Recreating a deleted local channel should be an explicit future action.

## Testing

Backend tests should cover real behavior and contracts:

- source URL normalization and validation;
- credential redaction in response DTOs;
- sub2api login/group/rate/key response decoding using deterministic fake HTTP servers;
- effective multiplier resolution;
- unknown multiplier blocks only that mapping, while explicit `0.0` remains valid;
- discovery upsert behavior and stale mapping marking;
- rediscovery preserves operator-owned and sync-owned mapping fields;
- successful discovery does not mutate local channels;
- sync creates one local channel per selected group;
- sync is idempotent and does not duplicate channels on rerun;
- concurrent sync claims are protected by a database-backed guarded transition;
- sync matches existing local channels by mapping `LocalChannelID`, not by channel name;
- sync preserves user-unowned channel fields;
- sync persists owned zero values such as `Priority=0`;
- sync records per-mapping errors without aborting the whole batch;
- generated channels are disabled or populated with models before abilities are created;
- no empty-model ability row is created;
- no local channel mutation on discovery failure.
- URL validation rejects non-HTTP schemes, empty hosts, and blocked internal targets;
- source outbound requests use the existing SSRF-aware validation/client path;
- stored errors are redacted and capped.

Use `testify/require` for setup and fatal assertions, and `testify/assert` for non-fatal value checks.

## Migration

Add the new tables through the existing model migration flow.

Migration must work on SQLite, MySQL, and PostgreSQL. Use GORM model definitions and indexes instead of raw database-specific SQL wherever possible.

Register new tables in both the normal migration path and the fast migration path.

Use bounded string column types for indexed string fields so MySQL can create the required indexes.

No existing channel rows need to be migrated.

## Future Work

Next likely task:

- Convert stored `EffectiveRateMultiplier` into priority recommendations or automatic priority updates.

Later tasks:

- Use the 7-day channel monitor availability history in priority or weight scoring.
- Add cache-rate scoring once cache metrics exist.
- Add new-api upstream source adapter.
- Add scheduled source discovery/sync.
- Add explicit upstream key cleanup flows.
- Add account-pool source type.

## Alternatives Considered

### Implement sub2api and new-api together

This gives broader source coverage, but it delays the first useful automation loop. It also risks designing around two still-changing adapter contracts before one implementation has proven the shared mapping model.

### Store upstream metadata in channel settings only

This is simpler at first, but it makes discovery previews, stale group tracking, idempotent sync, and future scoring harder. The mapping table is a better long-term boundary because a source group is not the same thing as a local channel setting.

### Start with price-to-priority directly

This would be premature. The system first needs reliable source discovery, durable source-channel mappings, and stored multipliers. Priority automation can then use those records without guessing where channels came from.

## Self Review

- The design keeps the first implementation useful without coupling it to the next scheduling algorithm.
- The design avoids storing upstream secrets in response DTOs and audit messages.
- The design keeps database storage compatible across SQLite, MySQL, and PostgreSQL.
- The design uses HTTP APIs for sub2api and avoids brittle dashboard automation.
- The design now separates discovery status from sync status and can distinguish an unknown multiplier from an explicit `0.0` multiplier.
- The main implementation risks are sub2api contract drift, plaintext-at-rest credential handling, source-owned channel field boundaries, and model population for generated channels. The implementation plan should verify each of these with targeted tests before broad UI work.
