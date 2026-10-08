package page

import (
	"fmt"
	"reflect"
	"testing"
)

func numbered(n int) []string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf("item-%d", i+1)
	}
	return items
}

func TestPaginateFirstPage(t *testing.T) {
	p := Paginate(numbered(5), 0, 2)
	if want := []string{"item-1", "item-2"}; !reflect.DeepEqual(p.Items, want) {
		t.Fatalf("items = %v, want %v", p.Items, want)
	}
	if p.NextCursor != 2 || !p.HasMore {
		t.Fatalf("next = %d more = %v, want 2 true", p.NextCursor, p.HasMore)
	}
}

func TestPaginatePartialLastPage(t *testing.T) {
	p := Paginate(numbered(5), 4, 2)
	if want := []string{"item-5"}; !reflect.DeepEqual(p.Items, want) {
		t.Fatalf("items = %v, want %v", p.Items, want)
	}
	if p.HasMore {
		t.Fatalf("last page reports HasMore = true")
	}
}

func TestPaginateCursorPastEnd(t *testing.T) {
	p := Paginate(numbered(3), 10, 2)
	if len(p.Items) != 0 {
		t.Fatalf("items = %v, want none", p.Items)
	}
}

func TestWalkVisitsEveryItemOnce(t *testing.T) {
	var seen []string
	for _, p := range Walk(numbered(5), 2) {
		seen = append(seen, p.Items...)
	}
	if !reflect.DeepEqual(seen, numbered(5)) {
		t.Fatalf("walk saw %v", seen)
	}
}
