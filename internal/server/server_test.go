package server

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/kubelogs/kubelogs/api/storagepb"
	"github.com/kubelogs/kubelogs/internal/storage"
	"github.com/kubelogs/kubelogs/internal/storage/sqlite"
)

func TestServer_WriteAndQuery(t *testing.T) {
	// Create in-memory SQLite store with small buffer to force immediate writes
	store, err := sqlite.New(sqlite.Config{Path: ":memory:", WriteBufferSize: 1})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Create server
	srv := New(store)

	// Start gRPC server
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	storagepb.RegisterStorageServiceServer(grpcServer, srv)

	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	// Create client
	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := storagepb.NewStorageServiceClient(conn)
	ctx := context.Background()

	// Write entries
	now := time.Now()
	entries := []*storagepb.LogEntry{
		{
			TimestampNanos: now.UnixNano(),
			Namespace:      "default",
			Pod:            "test-pod-1",
			Container:      "main",
			Severity:       uint32(storage.SeverityInfo),
			Message:        "Hello from test 1",
		},
		{
			TimestampNanos: now.Add(time.Second).UnixNano(),
			Namespace:      "default",
			Pod:            "test-pod-2",
			Container:      "main",
			Severity:       uint32(storage.SeverityError),
			Message:        "Error from test 2",
		},
	}

	writeResp, err := client.Write(ctx, &storagepb.WriteRequest{Entries: entries})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if writeResp.Count != 2 {
		t.Errorf("expected 2 entries written, got %d", writeResp.Count)
	}

	// Query all entries
	queryResp, err := client.Query(ctx, &storagepb.QueryRequest{
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(queryResp.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(queryResp.Entries))
	}

	// Query by namespace
	queryResp, err = client.Query(ctx, &storagepb.QueryRequest{
		Namespace: "default",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(queryResp.Entries) != 2 {
		t.Errorf("expected 2 entries in namespace default, got %d", len(queryResp.Entries))
	}

	// Query by severity
	queryResp, err = client.Query(ctx, &storagepb.QueryRequest{
		MinSeverity: uint32(storage.SeverityError),
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(queryResp.Entries) != 1 {
		t.Errorf("expected 1 error entry, got %d", len(queryResp.Entries))
	}
}

func TestServer_GetByID(t *testing.T) {
	store, err := sqlite.New(sqlite.Config{Path: ":memory:", WriteBufferSize: 1})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	srv := New(store)

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	storagepb.RegisterStorageServiceServer(grpcServer, srv)

	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := storagepb.NewStorageServiceClient(conn)
	ctx := context.Background()

	// Write an entry
	writeResp, err := client.Write(ctx, &storagepb.WriteRequest{
		Entries: []*storagepb.LogEntry{
			{
				TimestampNanos: time.Now().UnixNano(),
				Namespace:      "test",
				Pod:            "pod",
				Container:      "container",
				Message:        "test message",
			},
		},
	})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if writeResp.Count != 1 {
		t.Fatalf("expected 1 entry written, got %d", writeResp.Count)
	}

	// Query to get the ID
	queryResp, err := client.Query(ctx, &storagepb.QueryRequest{Limit: 1})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(queryResp.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(queryResp.Entries))
	}

	id := queryResp.Entries[0].Id

	// Get by ID
	getResp, err := client.GetByID(ctx, &storagepb.GetByIDRequest{Id: id})
	if err != nil {
		t.Fatalf("get by id failed: %v", err)
	}

	if getResp.Entry.Message != "test message" {
		t.Errorf("expected 'test message', got %q", getResp.Entry.Message)
	}

	// Get non-existent ID
	_, err = client.GetByID(ctx, &storagepb.GetByIDRequest{Id: 99999})
	if err == nil {
		t.Error("expected error for non-existent ID")
	}
}

func TestServer_Stats(t *testing.T) {
	store, err := sqlite.New(sqlite.Config{Path: ":memory:", WriteBufferSize: 1})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	srv := New(store)

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	storagepb.RegisterStorageServiceServer(grpcServer, srv)

	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := storagepb.NewStorageServiceClient(conn)
	ctx := context.Background()

	// Write some entries
	_, err = client.Write(ctx, &storagepb.WriteRequest{
		Entries: []*storagepb.LogEntry{
			{TimestampNanos: time.Now().UnixNano(), Namespace: "test", Pod: "pod", Container: "c", Message: "1"},
			{TimestampNanos: time.Now().UnixNano(), Namespace: "test", Pod: "pod", Container: "c", Message: "2"},
			{TimestampNanos: time.Now().UnixNano(), Namespace: "test", Pod: "pod", Container: "c", Message: "3"},
		},
	})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Get stats
	statsResp, err := client.Stats(ctx, &storagepb.StatsRequest{})
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}

	if statsResp.TotalEntries != 3 {
		t.Errorf("expected 3 total entries, got %d", statsResp.TotalEntries)
	}
}
