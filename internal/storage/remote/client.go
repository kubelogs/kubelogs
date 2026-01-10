package remote

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"github.com/kubelogs/kubelogs/api/storagepb"
	"github.com/kubelogs/kubelogs/internal/storage"
)

// Client is a remote storage client that implements storage.Store.
type Client struct {
	conn   *grpc.ClientConn
	client storagepb.StorageServiceClient
}

// NewClient creates a new remote storage client.
func NewClient(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second, // Ping server every 10s if idle
			Timeout:             5 * time.Second,  // Wait 5s for ping ack
			PermitWithoutStream: true,             // Send pings even with no active RPCs
		}),
	)
	if err != nil {
		return nil, err
	}

	return &Client{
		conn:   conn,
		client: storagepb.NewStorageServiceClient(conn),
	}, nil
}

// Write persists a batch of log entries.
func (c *Client) Write(ctx context.Context, entries storage.LogBatch) (int, error) {
	pbEntries := make([]*storagepb.LogEntry, len(entries))
	for i, e := range entries {
		pbEntries[i] = toProtoEntry(e)
	}

	resp, err := c.client.Write(ctx, &storagepb.WriteRequest{Entries: pbEntries})
	if err != nil {
		return 0, err
	}

	return int(resp.Count), nil
}

// Query searches for log entries matching the given criteria.
func (c *Client) Query(ctx context.Context, q storage.Query) (*storage.QueryResult, error) {
	req := &storagepb.QueryRequest{
		StartTimeNanos: q.StartTime.UnixNano(),
		EndTimeNanos:   q.EndTime.UnixNano(),
		Search:         q.Search,
		Namespace:      q.Namespace,
		Pod:            q.Pod,
		Container:      q.Container,
		MinSeverity:    uint32(q.MinSeverity),
		Attributes:     q.Attributes,
		Limit:          int32(q.Pagination.Limit),
		AfterId:        q.Pagination.AfterID,
		BeforeId:       q.Pagination.BeforeID,
		Order:          toProtoOrder(q.Pagination.Order),
	}

	resp, err := c.client.Query(ctx, req)
	if err != nil {
		return nil, err
	}

	entries := make([]storage.LogEntry, len(resp.Entries))
	for i, e := range resp.Entries {
		entries[i] = fromProtoEntry(e)
	}

	return &storage.QueryResult{
		Entries:       entries,
		HasMore:       resp.HasMore,
		NextCursor:    resp.NextCursor,
		TotalEstimate: resp.TotalEstimate,
	}, nil
}

// GetByID retrieves a single entry by its ID.
func (c *Client) GetByID(ctx context.Context, id int64) (*storage.LogEntry, error) {
	resp, err := c.client.GetByID(ctx, &storagepb.GetByIDRequest{Id: id})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}

	entry := fromProtoEntry(resp.Entry)
	return &entry, nil
}

// Delete removes entries older than the given timestamp.
func (c *Client) Delete(ctx context.Context, olderThan time.Time) (int64, error) {
	resp, err := c.client.Delete(ctx, &storagepb.DeleteRequest{
		OlderThanNanos: olderThan.UnixNano(),
	})
	if err != nil {
		return 0, err
	}

	return resp.DeletedCount, nil
}

// Stats returns storage statistics.
func (c *Client) Stats(ctx context.Context) (*storage.Stats, error) {
	resp, err := c.client.Stats(ctx, &storagepb.StatsRequest{})
	if err != nil {
		return nil, err
	}

	return &storage.Stats{
		TotalEntries:  resp.TotalEntries,
		DiskSizeBytes: resp.DiskSizeBytes,
		OldestEntry:   time.Unix(0, resp.OldestEntryNanos),
		NewestEntry:   time.Unix(0, resp.NewestEntryNanos),
	}, nil
}

// Close releases resources.
func (c *Client) Close() error {
	return c.conn.Close()
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

// toProtoOrder converts storage.Order to protobuf Order.
func toProtoOrder(o storage.Order) storagepb.Order {
	if o == storage.OrderAsc {
		return storagepb.Order_ORDER_ASC
	}
	return storagepb.Order_ORDER_DESC
}
