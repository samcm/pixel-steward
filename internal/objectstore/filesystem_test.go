package objectstore

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestFilesystemRoundTrip(t *testing.T) {
	store, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	object, err := store.Put(context.Background(), "leases/one/frame.png", "image/png", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if object.Size != 7 {
		t.Fatalf("size = %d", object.Size)
	}
	reader, _, err := store.Get(context.Background(), object.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	value, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "payload" {
		t.Fatalf("payload = %q", value)
	}
}

func TestFilesystemRejectsTraversal(t *testing.T) {
	store, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), "../escape", "text/plain", strings.NewReader("no")); err == nil {
		t.Fatal("traversal should fail")
	}
}
