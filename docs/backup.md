# Backup and restore

The database is the only durable state. Media lives in object storage and is backed up separately.

## What to back up

- The Postgres database, by physical backup with WAL archiving. `pg_dump` is fine for moving a small
  deployment but is not a point-in-time recovery story.
- The tenant signing keys, if they are held outside the database.

## Restoring

Restoring to the latest committed transaction is unremarkable: bring the server up against the
restored database and it continues.

## Point-in-time restore moves stream positions backwards

This is the case that needs care. Clients hold sync tokens containing a stream position. Restoring to
a point in the past rewinds the server's position, so a client can arrive holding a token from *after*
the restore point, naming a position the server has not reached.

Two consequences:

- A token ahead of the current position must be clamped to it. Without that, the affected client waits
  for a position that will not arrive for a while, and its sync appears to hang.
- Events between the restore point and the moment of failure are gone. Positions are handed out again,
  so a client that had already seen the originals may receive different events at the same positions.

If the rewind is more than a few seconds, invalidate client sessions and let clients start from a
fresh sync rather than trying to reconcile. It is the only way to guarantee no client carries a token
that disagrees with the server about what a position means.

## The signing master key

Private signing keys are stored sealed under `THAUMASTE_SIGNING_MASTER_KEY`. A database backup on
its own does not restore a working server: without that value every domain's signing key is
unreadable, no domain can sign, and the key endpoints stop answering. Back it up separately from the
database, or the pair is worthless to an attacker and to you alike.

To change it, stop the server, set `THAUMASTE_SIGNING_NEXT_MASTER_KEY` and run
`thaumaste keys reseal`. Every key is opened and round-tripped before any row is written and the
sweep is one transaction, so a wrong current key changes nothing. Then move the new value into
`THAUMASTE_SIGNING_MASTER_KEY` and start the server again. Published keys do not change and old
signatures stay verifiable.

## Rehearsing it

Restore into an empty environment and confirm the server starts, reports no pending migrations, and
serves. A restore procedure that has only been written down has not been tested.
