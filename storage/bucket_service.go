package storage

import (
	"context"
	"time"

	"github.com/influxdata/influxdb/v2"
	"github.com/influxdata/influxdb/v2/kit/tracing"
	"go.uber.org/zap"
)

type EngineSchema interface {
	CreateBucket(context.Context, *influxdb.Bucket) error
	UpdateBucketRetentionPeriod(context.Context, influxdb.ID, time.Duration) error
	DeleteBucket(context.Context, influxdb.ID, influxdb.ID) error
}

// BucketService wraps an existing influxdb.BucketService implementation.
//
// BucketService ensures that when a bucket is deleted, all stored data
// associated with the bucket is either removed, or marked to be removed via a
// future compaction.
type BucketService struct {
	influxdb.BucketService
	log    *zap.Logger
	engine EngineSchema
}

// NewBucketService returns a new BucketService for the provided EngineSchema,
// which typically will be an Engine.
func NewBucketService(logger *zap.Logger, s influxdb.BucketService, engine EngineSchema) *BucketService {
	return &BucketService{
		BucketService: s,
		engine:        engine,
		log:           logger,
	}
}

func (s *BucketService) CreateBucket(ctx context.Context, b *influxdb.Bucket) (err error) {
	span, ctx := tracing.StartSpanFromContext(ctx)
	defer span.Finish()

	defer func() {
		if err == nil {
			return
		}

		if b.ID.Valid() {
			if err := s.BucketService.DeleteBucket(ctx, b.ID); err != nil {
				s.log.Error("Unable to cleanup bucket after create failed", zap.Error(err))
			}
		}
	}()

	if err = s.BucketService.CreateBucket(ctx, b); err != nil {
		return err
	}

	if err = s.engine.CreateBucket(ctx, b); err != nil {
		return err
	}

	return nil
}

func (s *BucketService) UpdateBucket(ctx context.Context, id influxdb.ID, upd influxdb.BucketUpdate) (b *influxdb.Bucket, err error) {
	span, ctx := tracing.StartSpanFromContext(ctx)
	defer span.Finish()

	if upd.RetentionPeriod != nil {
		if err = s.engine.UpdateBucketRetentionPeriod(ctx, id, *upd.RetentionPeriod); err != nil {
			return nil, err
		}
	}

	return s.BucketService.UpdateBucket(ctx, id, upd)
}

// DeleteBucket removes a bucket by ID.
func (s *BucketService) DeleteBucket(ctx context.Context, bucketID influxdb.ID) error {
	span, ctx := tracing.StartSpanFromContext(ctx)
	defer span.Finish()

	bucket, err := s.FindBucketByID(ctx, bucketID)
	if err != nil {
		return err
	}

	// The data is dropped first from the storage engine. If this fails for any
	// reason, then the bucket will still be available in the future to retrieve
	// the orgID, which is needed for the engine.
	if err := s.engine.DeleteBucket(ctx, bucket.OrgID, bucketID); err != nil {
		return err
	}
	return s.BucketService.DeleteBucket(ctx, bucketID)
}
