package collect

import (
	"reflect"
	"testing"
)

// The API, the log, and the frontend share these names. Sync categories end
// a sync and are stored on the source. The others are recorded on the
// source's state, a folder, or a message, and the sync continues.
func TestCategories(t *testing.T) {
	tests := []struct {
		category Category
		name     string
		sync     bool
	}{
		{ServerNotReachable, "server_not_reachable", true},
		{SecureConnectionFailed, "secure_connection_failed", true},
		{SignInRejected, "sign_in_rejected", true},
		{PasswordUnreadable, "password_unreadable", false},
		{TimedOut, "timed_out", true},
		{ServerError, "server_error", true},
		{FolderUnavailable, "folder_unavailable", false},
		{StorageFull, "storage_full", true},
		{StorageWriteFailed, "storage_write_failed", true},
		{CatalogWriteFailed, "catalog_write_failed", true},
		{MessageUnavailable, "message_unavailable", false},
		{MessageTooLarge, "message_too_large", false},
		{MessageRemoved, "message_removed", false},
	}
	seen := map[string]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.category.String(); got != tt.name {
				t.Errorf("String() = %q, want %q", got, tt.name)
			}
			if got := tt.category.Sync(); got != tt.sync {
				t.Errorf("Sync() = %v, want %v", got, tt.sync)
			}
		})
		if seen[tt.category.String()] {
			t.Errorf("two categories are named %q", tt.category.String())
		}
		seen[tt.category.String()] = true
	}
}

// Another package can't name a private field, so it can't make a fourteenth
// category.
func TestCategoryIsClosed(t *testing.T) {
	category := reflect.TypeFor[Category]()
	if category.Kind() != reflect.Struct {
		t.Fatalf("Category is a %s, which other packages can convert to", category.Kind())
	}
	for field := range category.Fields() {
		if field.IsExported() {
			t.Errorf("Category exports the field %s", field.Name)
		}
	}
}
