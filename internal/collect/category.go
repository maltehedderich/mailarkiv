// Package collect archives IMAP mailboxes. It owns the imap_source,
// imap_folder, and imap_message_problem tables.
package collect

// Category is why a sync, a folder, or a message failed. Its field is
// private, so no other package can make another.
type Category struct {
	name string
	sync bool
}

// Sync categories end a sync and are stored as the source's last failure.
var (
	ServerNotReachable     = Category{"server_not_reachable", true}
	SecureConnectionFailed = Category{"secure_connection_failed", true}
	SignInRejected         = Category{"sign_in_rejected", true}
	TimedOut               = Category{"timed_out", true}
	ServerError            = Category{"server_error", true}
	StorageFull            = Category{"storage_full", true}
	StorageWriteFailed     = Category{"storage_write_failed", true}
	CatalogWriteFailed     = Category{"catalog_write_failed", true}
)

// The others leave the sync running. PasswordUnreadable is a source's state,
// FolderUnavailable a folder's, and the rest a message's.
var (
	PasswordUnreadable = Category{"password_unreadable", false}
	FolderUnavailable  = Category{"folder_unavailable", false}
	MessageUnavailable = Category{"message_unavailable", false}
	MessageTooLarge    = Category{"message_too_large", false}
	MessageRemoved     = Category{"message_removed", false}
)

// String returns the category's name, which the API, the log, and the
// frontend share.
func (c Category) String() string { return c.name }

// Sync reports whether the category ends a sync.
func (c Category) Sync() bool { return c.sync }
