package postgres

import (
	"context"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"

	"github.com/authzed/spicedb/internal/datastore"
	"github.com/authzed/spicedb/internal/datastore/common"
	"github.com/authzed/spicedb/internal/datastore/options"
)

var queryTuples = psql.Select(
	colNamespace,
	colObjectID,
	colRelation,
	colUsersetNamespace,
	colUsersetObjectID,
	colUsersetRelation,
).From(tableTuple)

var schema = common.SchemaInformation{
	ColNamespace:        colNamespace,
	ColObjectID:         colObjectID,
	ColRelation:         colRelation,
	ColUsersetNamespace: colUsersetNamespace,
	ColUsersetObjectID:  colUsersetObjectID,
	ColUsersetRelation:  colUsersetRelation,
}

func (pgd *pgDatastore) QueryTuples(
	ctx context.Context,
	filter *v1.RelationshipFilter,
	revision datastore.Revision,
	opts ...options.QueryOptionsOption,
) (iter datastore.TupleIterator, err error) {
	qBuilder := common.NewSchemaQueryFilterer(schema, filterToLivingObjects(queryTuples, revision)).
		FilterToResourceType(filter.ResourceType)

	if filter.OptionalResourceId != "" {
		qBuilder = qBuilder.FilterToResourceID(filter.OptionalResourceId)
	}

	if filter.OptionalRelation != "" {
		qBuilder = qBuilder.FilterToRelation(filter.OptionalRelation)
	}

	if filter.OptionalSubjectFilter != nil {
		qBuilder = qBuilder.FilterToSubjectFilter(filter.OptionalSubjectFilter)
	}

	queryOpts := options.NewQueryOptionsWithOptions(opts...)

	ctq := common.TupleQuerySplitter{
		Conn:                      pgd.dbpool,
		PrepareTransaction:        nil,
		SplitAtEstimatedQuerySize: pgd.splitAtEstimatedQuerySize,

		FilteredQueryBuilder: qBuilder,
		Revision:             revision,
		Limit:                queryOpts.Limit,
		Usersets:             queryOpts.Usersets,

		Tracer:    tracer,
		DebugName: "QueryTuples",
	}

	return ctq.SplitAndExecute(ctx)
}

func (pgd *pgDatastore) ReverseQueryTuples(
	ctx context.Context,
	subjectFilter *v1.SubjectFilter,
	revision datastore.Revision,
	opts ...options.ReverseQueryOptionsOption,
) (iter datastore.TupleIterator, err error) {
	qBuilder := common.NewSchemaQueryFilterer(schema, filterToLivingObjects(queryTuples, revision)).
		FilterToSubjectFilter(subjectFilter)

	queryOpts := options.NewReverseQueryOptionsWithOptions(opts...)

	if queryOpts.ResRelation != nil {
		qBuilder = qBuilder.
			FilterToResourceType(queryOpts.ResRelation.Namespace).
			FilterToRelation(queryOpts.ResRelation.Relation)
	}

	ctq := common.TupleQuerySplitter{
		Conn:                      pgd.dbpool,
		PrepareTransaction:        nil,
		SplitAtEstimatedQuerySize: pgd.splitAtEstimatedQuerySize,

		FilteredQueryBuilder: qBuilder,
		Revision:             revision,
		Limit:                queryOpts.ReverseLimit,
		Usersets:             nil,

		Tracer:    tracer,
		DebugName: "ReverseQueryTuples",
	}

	return ctq.SplitAndExecute(ctx)
}
