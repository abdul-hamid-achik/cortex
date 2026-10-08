// Package page splits an ordered list into fixed-size pages addressed by an
// opaque offset cursor.
package page

// DefaultSize is the page size used when the caller does not supply one.
const DefaultSize = 20

// Page is one window over a list together with the cursor for the next one.
type Page struct {
	Items      []string
	NextCursor int
	HasMore    bool
}

// Paginate returns the page of items that starts at cursor. A cursor past the
// end of the list yields an empty page, and a non-positive size falls back to
// DefaultSize.
func Paginate(items []string, cursor, size int) Page {
	if size <= 0 {
		size = DefaultSize
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(items) {
		cursor = len(items)
	}
	end := cursor + size
	if end > len(items) {
		end = len(items)
	}
	return Page{
		Items:      items[cursor:end],
		NextCursor: end,
		HasMore:    end <= len(items),
	}
}
