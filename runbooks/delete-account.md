# How to delete user with all data

Log into production db, then drop into psql

```bash
cd pico
make psql
```

```sql
delete from app_users where name = 'xxx';
```

This only removes data from the database, we also have static assets (prose images, pgs assets).

We have a clean bucket script for prose and pgs that we run periodically to remove static assets from orphaned buckets.

SSH into pgs or prose VMs then run:

```bash
make scripts
FS_STORAGE_DIR="./data/storage" go run ./cmd/scripts/clean-buckets
```

Finally, wipe the pgs cache so any sites that were deleted are actually removed:

```bash
ssh pgs.sh cache-all
```
