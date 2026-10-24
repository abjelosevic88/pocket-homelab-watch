# Backup

Everything Pocket Watch keeps is in `DATA_DIR` (`/data` in the container):

| File | What it is |
|---|---|
| `pocket-watch.db` (+ `-wal`, `-shm`) | History, watchlists, followed requests, settings, accounts |
| `secret.key` | Seals the Jellyfin tokens people signed in with. Lose it and they sign in again; nothing else is lost. |
| `core.token`, `core.token.oidc.json` | The app's Pocket Core registration (re-created by registering again) |
| `backups/` | Snapshots made by `pocket-watch backup` or Settings → Backup & data |

Your films, shows, resume points, watched marks and favourites live in Jellyfin — back that up there. History
before Pocket Watch was installed can always be read again from Jellyfin (Settings → Jellyfin → Read everything
again), but only as one entry per finished title.

**Never copy the live database file.** Use one of:

```sh
docker exec pocket-watch pocket-watch backup            # writes data/backups/pocket-watch-<time>.db
curl -H "Authorization: Bearer pwt_…" https://watch.example.com/api/v1/backup -o watch.db   # admins
```

Each person can export their own history and watchlist as JSON under Settings → Backup & data, and import it on
another box.

To restore: stop the app, replace `pocket-watch.db` with a snapshot (delete stale `-wal`/`-shm`), start it.
