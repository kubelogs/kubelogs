package server

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/kubelogs/kubelogs/api/storagepb"
	"github.com/kubelogs/kubelogs/internal/storage"
)

// Server implements the StorageService gRPC server.
type Server struct {
	storagepb.UnimplementedStorageServiceServer
	store storage.Store
}

// New creates a new gRPC server wrapping the given store.
func New(store storage.Store) *Server {
	return &Server{store: store}
}

// Write persists a batch of log entries.
func (s *Server) Write(ctx context.Context, req *storagepb.WriteRequest) (*storagepb.WriteResponse, error) {
	entries := make(storage.LogBatch, len(req.Entries))
	for i, e := range req.Entries {
		entries[i] = fromProtoEntry(e)
	}

	n, err := s.store.Write(ctx, entries)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "write failed: %v", err)
	}

	return &storagepb.WriteResponse{Count: int32(n)}, nil
}

// Query searches for log entries matching the given criteria.
func (s *Server) Query(ctx context.Context, req *storagepb.QueryRequest) (*storagepb.QueryResponse, error) {
	q := storage.Query{
		Search:      req.Search,
		Namespace:   req.Namespace,
		Pod:         req.Pod,
		Container:   req.Container,
		MinSeverity: storage.Severity(req.MinSeverity),
		Attributes:  req.Attributes,
		Pagination: storage.Pagination{
			Limit:    int(req.Limit),
			AfterID:  req.AfterId,
			BeforeID: req.BeforeId,
			Order:    fromProtoOrder(req.Order),
		},
	}

	// Only set time filters if non-zero (zero means no filter)
	if req.StartTimeNanos != 0 {
		q.StartTime = time.Unix(0, req.StartTimeNanos)
	}
	if req.EndTimeNanos != 0 {
		q.EndTime = time.Unix(0, req.EndTimeNanos)
	}

	result, err := s.store.Query(ctx, q)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "query failed: %v", err)
	}

	pbEntries := make([]*storagepb.LogEntry, len(result.Entries))
	for i, e := range result.Entries {
		pbEntries[i] = toProtoEntry(e)
	}

	return &storagepb.QueryResponse{
		Entries:       pbEntries,
		HasMore:       result.HasMore,
		NextCursor:    result.NextCursor,
		TotalEstimate: result.TotalEstimate,
	}, nil
}

// GetByID retrieves a single entry by its ID.
func (s *Server) GetByID(ctx context.Context, req *storagepb.GetByIDRequest) (*storagepb.GetByIDResponse, error) {
	entry, err := s.store.GetByID(ctx, req.Id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "entry not found")
		}
		return nil, status.Errorf(codes.Internal, "get by id failed: %v", err)
	}

	return &storagepb.GetByIDResponse{Entry: toProtoEntry(*entry)}, nil
}

// Delete removes entries older than the given timestamp.
func (s *Server) Delete(ctx context.Context, req *storagepb.DeleteRequest) (*storagepb.DeleteResponse, error) {
	olderThan := time.Unix(0, req.OlderThanNanos)

	count, err := s.store.Delete(ctx, olderThan)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete failed: %v", err)
	}

	return &storagepb.DeleteResponse{DeletedCount: count}, nil
}

// Stats returns storage statistics.
func (s *Server) Stats(ctx context.Context, req *storagepb.StatsRequest) (*storagepb.StatsResponse, error) {
	stats, err := s.store.Stats(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "stats failed: %v", err)
	}

	return &storagepb.StatsResponse{
		TotalEntries:     stats.TotalEntries,
		DiskSizeBytes:    stats.DiskSizeBytes,
		OldestEntryNanos: stats.OldestEntry.UnixNano(),
		NewestEntryNanos: stats.NewestEntry.UnixNano(),
	}, nil
}

// toProtoEntry converts a storage.LogEntry to protobuf.
func toProtoEntry(e storage.LogEntry) *storagepb.LogEntry {
	return &storagepb.LogEntry{
		Id:             e.ID,
		TimestampNanos: e.Timestamp.UnixNano(),
		Namespace:      e.Namespace,
		Pod:            e.Pod,
		Container:      e.Container,
		Severity:       uint32(e.Severity),
		Message:        e.Message,
		Attributes:     e.Attributes,
	}
}

// fromProtoEntry converts a protobuf LogEntry to storage.LogEntry.
func fromProtoEntry(e *storagepb.LogEntry) storage.LogEntry {
	return storage.LogEntry{
		ID:         e.Id,
		Timestamp:  time.Unix(0, e.TimestampNanos),
		Namespace:  e.Namespace,
		Pod:        e.Pod,
		Container:  e.Container,
		Severity:   storage.Severity(e.Severity),
		Message:    e.Message,
		Attributes: e.Attributes,
	}
}

// fromProtoOrder converts protobuf Order to storage.Order.
func fromProtoOrder(o storagepb.Order) storage.Order {
	if o == storagepb.Order_ORDER_ASC {
		return storage.OrderAsc
	}
	return storage.OrderDesc
}
