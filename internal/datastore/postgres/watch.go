package postgres

import (
	"context"
	"errors"
	"time"

	sq "github.com/Masterminds/squirrel"
	v0 "github.com/authzed/authzed-go/proto/authzed/api/v0"

	"github.com/authzed/spicedb/internal/datastore"
	"github.com/authzed/spicedb/internal/datastore/common"
)

const (
	watchSleep = 100 * time.Millisecond
)

var queryChanged = psql.Select(
	colNamespace,
	colObjectID,
	colRelation,
	colUsersetNamespace,
	colUsersetObjectID,
	colUsersetRelation,
	colCreatedTxn,
	colDeletedTxn,
).From(tableTuple)

func (pgd *pgDatastore) Watch(ctx context.Context, afterRevision datastore.Revision) (<-chan *datastore.RevisionChanges, <-chan error) {
	updates := make(chan *datastore.RevisionChanges, pgd.watchBufferLength)
	errs := make(chan error, 1)

	go func() {
		defer close(updates)
		defer close(errs)

		currentTxn := transactionFromRevision(afterRevision)

		for {
			var stagedUpdates []*datastore.RevisionChanges
			var err error
			stagedUpdates, currentTxn, err = pgd.loadChanges(ctx, currentTxn)
			if err != nil {
				if errors.Is(ctx.Err(), context.Canceled) {
					errs <- datastore.NewWatchCanceledErr()
				} else {
					errs <- err
				}
				return
			}

			// Write the staged updates to the channel
			for _, changeToWrite := range stagedUpdates {
				select {
				case updates <- changeToWrite:
				default:
					errs <- datastore.NewWatchDisconnectedErr()
					return
				}
			}

			// If there were no changes, sleep a bit
			if len(stagedUpdates) == 0 {
				sleep := time.NewTimer(watchSleep)

				select {
				case <-sleep.C:
					break
				case <-ctx.Done():
					errs <- datastore.NewWatchCanceledErr()
					return
				}
			}
		}
	}()

	return updates, errs
}

func (pgd *pgDatastore) loadChanges(
	ctx context.Context,
	afterRevision uint64,
) (changes []*datastore.RevisionChanges, newRevision uint64, err error) {
	newRevision, err = pgd.loadRevision(ctx)
	if err != nil {
		return
	}

	if newRevision == afterRevision {
		return
	}

	sql, args, err := queryChanged.Where(sq.Or{
		sq.And{
			sq.Gt{colCreatedTxn: afterRevision},
			sq.LtOrEq{colCreatedTxn: newRevision},
		},
		sq.And{
			sq.Gt{colDeletedTxn: afterRevision},
			sq.LtOrEq{colDeletedTxn: newRevision},
		},
	}).ToSql()
	if err != nil {
		return
	}

	rows, err := pgd.dbpool.Query(ctx, sql, args...)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			err = datastore.NewWatchCanceledErr()
		}
		return
	}

	stagedChanges := common.NewChanges()

	for rows.Next() {
		userset := &v0.ObjectAndRelation{}
		tpl := &v0.RelationTuple{
			ObjectAndRelation: &v0.ObjectAndRelation{},
			User: &v0.User{
				UserOneof: &v0.User_Userset{
					Userset: userset,
				},
			},
		}

		var createdTxn uint64
		var deletedTxn uint64
		err = rows.Scan(
			&tpl.ObjectAndRelation.Namespace,
			&tpl.ObjectAndRelation.ObjectId,
			&tpl.ObjectAndRelation.Relation,
			&userset.Namespace,
			&userset.ObjectId,
			&userset.Relation,
			&createdTxn,
			&deletedTxn,
		)
		if err != nil {
			return
		}

		if createdTxn > afterRevision && createdTxn <= newRevision {
			stagedChanges.AddChange(ctx, createdTxn, tpl, v0.RelationTupleUpdate_TOUCH)
		}

		if deletedTxn > afterRevision && deletedTxn <= newRevision {
			stagedChanges.AddChange(ctx, deletedTxn, tpl, v0.RelationTupleUpdate_DELETE)
		}
	}
	if err = rows.Err(); err != nil {
		return
	}

	changes = stagedChanges.AsRevisionChanges()

	return
}
