# Deployment

Operator notes for running TheChannel. The reference deployment is CapRover with
the database as its own app; `docker-compose.yml` in this repo describes a
different, single-host layout and is **not** what CapRover uses — see
[The `/app/files` trap](#the-appfiles-trap) for why that difference matters.

CapRover builds from `captain-definition`, which points at the `Dockerfile`.
Changing an environment variable restarts the container with the **existing**
image; it does not rebuild. New code requires a Deploy, not a restart.

---

## 1. The database app

The backend reaches the database purely through environment variables, so the
database living in its own app is a configuration fact and needs no code change.

| Variable | Value on CapRover |
|---|---|
| `REDIS_ADDR` | `srv-captain--<dbapp>:6666` |
| `REDIS_PROTOCOL` | `tcp` |
| `REDIS_PASSWORD` | only if the database requires one |
| `REDIS_MASTER` | only when using Redis Sentinel |

`srv-captain--<appname>` is how CapRover apps reach each other on the internal
network. `6666` is Apache Kvrocks' default port — `kvrocks.conf` in this repo
sets no `port` line, so the default applies. If you changed it, use your value.

**`REDIS_PROTOCOL` must be `tcp`.** Note that `sample.env` shows

```
REDIS_ADDR=/app/data/kvrocks.sock
REDIS_PROTOCOL=unix
```

That is correct only for the docker-compose layout, where both containers share
a volume. A unix socket cannot cross container boundaries, so copying those two
lines into a CapRover setup with a separate database app cannot work. The code
defaults `REDIS_PROTOCOL` to `tcp` when unset, but an explicitly wrong value is
fatal at boot with `dial: unknown network unix`.

### Verify the database has a persistent volume

The database app must have a persistent directory mapped to its data directory
(`/var/lib/kvrocks` for Kvrocks). Without it, every channel, message, role and
setting is destroyed on the next redeploy of the **database** app.

In CapRover: open the database app → App Configs → Persistent Directories, and
confirm one is mapped to the data path. If the list is empty, add it before you
put anything real in the system.

---

## 2. Cloudflare R2 (file storage)

Set all four of these, or R2 stays off entirely and files are written to local
disk:

| Variable | Where it comes from |
|---|---|
| `R2_ACCOUNT_ID` | Cloudflare dashboard → R2 → Account ID (right-hand panel) |
| `R2_ACCESS_KEY_ID` | R2 → Manage API Tokens → Create API Token |
| `R2_SECRET_ACCESS_KEY` | shown once, alongside the Access Key ID |
| `R2_BUCKET_NAME` | the bucket you created |

The token must have **Object Read & Write** permission and its scope must
include the bucket. A read-only or wrongly scoped token authenticates fine and
then fails every upload with `AccessDenied` — see [Diagnosing an upload
failure](#diagnosing-an-upload-failure).

**Take the Access Key ID and Secret Access Key, not the token value.** The
create-token page shows a `cfat_…` token first and the S3 credential pair below
it. The `cfat_` token is a Cloudflare REST API bearer token and is not used by
this application, which speaks the S3 protocol.

### `R2_PUBLIC_URL` — optional, and usually leave it unset

| Unset (recommended) | Set |
|---|---|
| The backend issues a pre-signed URL valid for one hour and redirects the browser to it. The bucket stays private. | The backend redirects to the public bucket URL. |

A public bucket URL is readable by anyone who has the link, **regardless of the
channel's "require authentication to view files" setting**. Setting this
silently overrides that privacy choice for every channel on the platform. Leave
it unset unless you specifically want a public CDN.

No Cloudflare Worker is required in either mode.

### Migrating files that already exist

Blobs written while R2 was off are on local disk only. A one-shot migration
copies them into the bucket on the first boot after R2 is configured. Its log
lines look like:

```
migrations: running 2026-08-local-files-to-r2
migrations: local-files-to-R2: 12 uploaded, 0 already in R2, 0 missing locally, 0 malformed, 0 failed, out of 12 file records across 3 channels
migrations: 2026-08-local-files-to-r2 completed
```

`0 failed` and `completed` together mean it is done. Anything else leaves the
migration **unmarked**, so it retries automatically on the next boot — a failed
run costs nothing and loses nothing.

Local copies are never deleted, and a file the migration has not reached yet is
still served from disk, so switching R2 on cannot break historical images.

---

## 3. Environment variables

### Required

| Variable | Notes |
|---|---|
| `SERVER_PORT` | The port the backend listens on. There is no default — leaving it unset binds an arbitrary port and nothing can reach the app. |
| `SECRET_KEY` | Signs session cookies. Changing it invalidates every active session and logs everyone out. |
| `REDIS_ADDR` | See [The database app](#1-the-database-app). |
| `GOOGLE_CLIENT_ID` | Google OAuth. Sign-in is the only way in, so without these nobody can log in. |
| `GOOGLE_CLIENT_SECRET` | |
| `ADMIN_USERS` | Comma-separated emails granted super-admin. Without at least one, no one can reach the super-admin panel. |

### Optional

| Variable | Default | Notes |
|---|---|---|
| `REDIS_PROTOCOL` | `tcp` | Must be `tcp` for a separate database app. |
| `REDIS_PASSWORD` | empty | |
| `REDIS_MASTER` | empty | Redis Sentinel master name. |
| `ROOT_STATIC_FOLDER` | `/usr/share/ng` | Where the built frontend is served from. The Dockerfile already puts it there. |
| `COOKIE_INSECURE` | unset | Set to `1` **only** for local plain-HTTP development. Unset, the session cookie is marked `Secure` and will not survive a non-HTTPS origin. |
| `R2_*` | unset | See [Cloudflare R2](#2-cloudflare-r2-file-storage). |
| `SKIP_MIGRATIONS` | unset | `1` skips all data migrations at boot. An escape hatch, not a normal setting. |
| `MIGRATION_APPLY_AUTH_FLAGS` | unset | `1` makes the features backfill honour leftover `require_auth`, `require_auth_for_view_files` and `count_views` settings values. Off by default because those keys are no longer in the owner UI, so applying them would restrict channels in a way their owners cannot see or undo. |
| `ALLOW_LEGACY_UNSCOPED_FILES` | unset | `true` lets file records with no owning channel be served from any channel. This reopens a cross-tenant read path and exists only as a temporary escape hatch during a backfill. |
| `PPROF_ADDR` | unset | e.g. `localhost:6060` to enable the Go profiler. Never expose it publicly. |

---

## 4. The `/app/files` trap

With R2 off, uploads are written to `/app/files/` inside the container.

`docker-compose.yml` maps `./channel_data:/app/files`, which makes that path
durable — but **CapRover does not use docker-compose**. It builds from
`captain-definition` and the `Dockerfile`, and mounts no volume there unless you
add one explicitly. So on a default CapRover deployment every uploaded file
lands in the container's writable layer and is destroyed on the next deploy, and
the upload fails outright if that layer is full or read-only.

Either configure R2, or add a persistent directory mapped to `/app/files` in the
backend app's App Configs. R2 is the better answer: it also survives running
more than one backend replica, which a local directory does not.

---

## 5. Diagnosing an upload failure

An upload returning 500 logs the reason. Find the line beginning `uploadFile:`:

```
uploadFile: raam: R2 upload of file 8759a37 failed (bucket "arootz",
key files/0d/fb/0dfb51…, 31126 bytes, content-type image/jpeg):
… api error AccessDenied: Access Denied
```

The S3 error code at the end is the diagnosis:

| Code | Cause |
|---|---|
| `AccessDenied` | The token authenticated but cannot write — created read-only, or scoped to a different bucket. |
| `NoSuchBucket` | `R2_BUCKET_NAME` or `R2_ACCOUNT_ID` is wrong. |
| `InvalidAccessKeyId` | `R2_ACCESS_KEY_ID` is wrong. |
| `SignatureDoesNotMatch` | `R2_SECRET_ACCESS_KEY` is wrong. |

Do not change variables before reading this line — all four failures look
identical from the browser, which only ever sees a 500.

### Telling where a file was served from

A request to `/api/channel/<slug>/files/<id>` answers:

- **302** with a tiny body — redirected to R2 (pre-signed, or the public URL).
- **200** with the file's real size — served from local disk. The log also
  prints `serveFile: … not in R2 …, serving local copy`.

A browser showing the image proves neither; the status code does.

---

## 6. Boot log checklist

A healthy start prints, in order:

```
Connection to DB successful!
R2 storage enabled (bucket: <name>)        # absent when R2 is off, which is a valid state
migrations: running <id>                    # only for migrations not yet applied
migrations: <id> completed
```

`migrations: channel features backfill touched N of M channels` should report an
`M` matching your real channel count. If it does not, stop and investigate
before continuing — the migrations are one-shot.
