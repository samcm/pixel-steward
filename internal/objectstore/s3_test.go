package objectstore

import (
	"context"
	"testing"
)

func TestNewS3DoesNotProbeEndpoint(t *testing.T) {
	// Startup must not depend on object storage availability. Object operations
	// perform the bucket check and retry it after transient failures.
	value, err := NewS3(context.Background(), S3Config{
		Endpoint:  "http://127.0.0.1:1",
		Region:    "auto",
		Bucket:    "frames",
		AccessKey: "test",
		SecretKey: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if value == nil || value.ready {
		t.Fatalf("unexpected new S3 state: %+v", value)
	}
}
