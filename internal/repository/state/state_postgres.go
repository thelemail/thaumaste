package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/repository/intern"
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.State {
	return &repo{db: db}
}

func (r *repo) Save(ctx context.Context, roomNID int64, state entity.StateMap) (int64, error) {
	exec := r.db.Querier(ctx)
	hash := state.SnapshotHash()

	existing, err := dbpg.StateSnapshots(
		dbpg.StateSnapshotWhere.RoomNid.EQ(roomNID),
		dbpg.StateSnapshotWhere.SnapshotHash.EQ(hash[:]),
	).One(ctx, exec)
	switch {
	case err == nil:
		return existing.StateSnapshotNid, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("repository: look up state snapshot: %w", err)
	}

	blockNID, err := r.saveBlock(ctx, roomNID, state)
	if err != nil {
		return 0, err
	}

	snapshot := dbpg.StateSnapshot{RoomNid: roomNID, SnapshotHash: hash[:]}
	if err := snapshot.Insert(ctx, exec, boil.Infer()); err != nil {
		return 0, fmt.Errorf("repository: insert state snapshot: %w", err)
	}
	link := dbpg.StateSnapshotBlock{
		StateSnapshotNid: snapshot.StateSnapshotNid,
		Ordinal:          0,
		StateBlockNid:    blockNID,
	}
	if err := link.Insert(ctx, exec, boil.Infer()); err != nil {
		return 0, fmt.Errorf("repository: link state block: %w", err)
	}
	return snapshot.StateSnapshotNid, nil
}

func (r *repo) saveBlock(ctx context.Context, roomNID int64, state entity.StateMap) (int64, error) {
	exec := r.db.Querier(ctx)
	hash := state.SnapshotHash()

	existing, err := dbpg.StateBlocks(
		dbpg.StateBlockWhere.RoomNid.EQ(roomNID),
		dbpg.StateBlockWhere.BlockHash.EQ(hash[:]),
	).One(ctx, exec)
	switch {
	case err == nil:
		return existing.StateBlockNid, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("repository: look up state block: %w", err)
	}

	block := dbpg.StateBlock{RoomNid: roomNID, BlockHash: hash[:]}
	if err := block.Insert(ctx, exec, boil.Infer()); err != nil {
		return 0, fmt.Errorf("repository: insert state block: %w", err)
	}

	for _, key := range state.Keys() {
		event := state[key]
		stored, err := dbpg.Events(dbpg.EventWhere.EventID.EQ(event.ID())).One(ctx, exec)
		if err != nil {
			return 0, fmt.Errorf("repository: state names an unstored event %s: %w", event.ID(), err)
		}
		typeNID, err := intern.EventType(ctx, exec, key.Type)
		if err != nil {
			return 0, err
		}
		stateKeyNID, err := intern.StateKey(ctx, exec, key.StateKey)
		if err != nil {
			return 0, err
		}
		entry := dbpg.StateBlockEntry{
			StateBlockNid:    block.StateBlockNid,
			EventTypeNid:     typeNID,
			EventStateKeyNid: stateKeyNID,
			EventNid:         stored.EventNid,
		}
		if err := entry.Insert(ctx, exec, boil.Infer()); err != nil {
			return 0, fmt.Errorf("repository: insert state entry: %w", err)
		}
	}
	return block.StateBlockNid, nil
}

func (r *repo) Load(ctx context.Context, snapshotNID int64) (entity.StateMap, error) {
	if snapshotNID == 0 {
		return entity.StateMap{}, nil
	}
	exec := r.db.Querier(ctx)

	links, err := dbpg.StateSnapshotBlocks(
		dbpg.StateSnapshotBlockWhere.StateSnapshotNid.EQ(snapshotNID),
		qm.OrderBy(dbpg.StateSnapshotBlockColumns.Ordinal),
	).All(ctx, exec)
	if err != nil {
		return nil, fmt.Errorf("repository: load snapshot blocks: %w", err)
	}
	if len(links) == 0 {
		return nil, repository.ErrStateSnapshotNotFound
	}

	out := entity.StateMap{}
	for _, link := range links {
		entries, err := dbpg.StateBlockEntries(
			dbpg.StateBlockEntryWhere.StateBlockNid.EQ(link.StateBlockNid),
			qm.Load(dbpg.StateBlockEntryRels.EventTypeNidEventType),
			qm.Load(dbpg.StateBlockEntryRels.EventStateKeyNidEventStateKey),
			qm.Load(qm.Rels(dbpg.StateBlockEntryRels.EventNidEvent, dbpg.EventRels.RoomNidRoom)),
		).All(ctx, exec)
		if err != nil {
			return nil, fmt.Errorf("repository: load state entries: %w", err)
		}
		for _, entry := range entries {
			key, event, err := toStateEntry(entry)
			if err != nil {
				return nil, err
			}
			out[key] = event
		}
	}
	return out, nil
}

func toStateEntry(entry *dbpg.StateBlockEntry) (entity.StateKey, entity.Event, error) {
	if entry.R == nil || entry.R.EventNidEvent == nil ||
		entry.R.EventTypeNidEventType == nil || entry.R.EventStateKeyNidEventStateKey == nil {
		return entity.StateKey{}, entity.Event{}, repository.ErrStateSnapshotNotFound
	}
	stored := entry.R.EventNidEvent
	if stored.R == nil || stored.R.RoomNidRoom == nil {
		return entity.StateKey{}, entity.Event{}, repository.ErrStateSnapshotNotFound
	}
	version, err := entity.LookupRoomVersion(entity.RoomVersionID(stored.R.RoomNidRoom.RoomVersion))
	if err != nil {
		return entity.StateKey{}, entity.Event{}, err
	}
	event, err := entity.NewEventFromJSON(stored.EventJSON, version)
	if err != nil {
		return entity.StateKey{}, entity.Event{}, fmt.Errorf("repository: read state event: %w", err)
	}
	return entity.StateKey{
		Type:     entry.R.EventTypeNidEventType.EventType,
		StateKey: entry.R.EventStateKeyNidEventStateKey.EventStateKey,
	}, event, nil
}
