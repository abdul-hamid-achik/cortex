package page

// Walk follows cursors from the start of items and returns every page in
// order. It stops as soon as a page reports that nothing follows it, or when a
// page comes back empty so a misbehaving cursor can never loop forever.
func Walk(items []string, size int) []Page {
	var pages []Page
	cursor := 0
	for {
		p := Paginate(items, cursor, size)
		pages = append(pages, p)
		if !p.HasMore || len(p.Items) == 0 {
			return pages
		}
		cursor = p.NextCursor
	}
}
