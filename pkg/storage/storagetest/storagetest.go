// Package storagetest provides storage helpers for tests.
package storagetest

import (
	"testing"

	"github.com/picosh/pico/pkg/storage"
)

// Bucket creates an empty bucket and removes it when the test ends.
func Bucket(t testing.TB, st storage.StorageServe, name string) storage.Bucket {
	t.Helper()
	if b, err := st.GetBucket(name); err == nil {
		if err := st.DeleteBucket(b); err != nil {
			t.Fatal(err)
		}
	}
	b, err := st.UpsertBucket(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteBucket(b) })
	return b
}
