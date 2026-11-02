package http

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/views"
)

type pagedAPI struct {
	fakeAPI
	promises []views.Promise
	books    []views.Book
}

func (p *pagedAPI) ListPromisesByOrder(context.Context, string) (views.PromiseList, error) {
	return views.PromiseList{Items: p.promises}, nil
}

func (p *pagedAPI) ListPromiseBooks(context.Context, string, string) (views.BookList, error) {
	return views.BookList{Items: p.books}, nil
}

type pageBody struct {
	Items []struct {
		PromiseID string `json:"promiseId"`
		BookID    string `json:"bookId"`
	} `json:"items"`
	NextCursor string `json:"nextCursor"`
}

// walk follows nextCursor with the given limit and returns every id in order.
func walk(t *testing.T, api *pagedAPI, path string, limit int) []string {
	t.Helper()
	h := NewRouter(&Server{API: api}, nil, "")
	var ids []string
	cursor := ""
	for i := 0; i < 50; i++ {
		url := fmt.Sprintf("%slimit=%d", path, limit)
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		rec := do(h, "GET", url, ``)
		if rec.Code != 200 {
			t.Fatalf("%s = %d %s", url, rec.Code, rec.Body)
		}
		var b pageBody
		if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		for _, it := range b.Items {
			ids = append(ids, it.PromiseID+it.BookID)
		}
		if b.NextCursor == "" {
			return ids
		}
		cursor = b.NextCursor
	}
	t.Fatal("never reached the last page")
	return nil
}

func TestPromisesWalkAllPagesWithoutDuplicatesOrSkips(t *testing.T) {
	api := &pagedAPI{}
	var want []string
	for i := 1; i <= 7; i++ { // deliberately shuffled input order
		api.promises = append([]views.Promise{{PromiseID: fmt.Sprintf("prm-%d", i), GroupNo: i}}, api.promises...)
		want = append(want, fmt.Sprintf("prm-%d", i))
	}
	got := walk(t, api, "/promises?orderId=ord-1&", 3)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("walked %v, want %v", got, want)
	}
}

func TestBooksWalkAllPagesWithTiesOnCutoff(t *testing.T) {
	api := &pagedAPI{}
	at := time.Date(2026, 10, 22, 18, 0, 0, 0, time.UTC)
	var want []string
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("N:dep-%d", i)
		api.books = append(api.books, views.Book{BookID: id, CutoffAt: at.Add(time.Duration(i/2) * time.Hour)})
		want = append(want, id)
	}
	got := walk(t, api, "/promise-books?nodeId=N&date=2026-10-22&", 2)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("walked %v, want %v", got, want)
	}
}

func TestCollectionQueryErrors(t *testing.T) {
	h := NewRouter(&Server{API: &pagedAPI{}}, nil, "")
	cases := []struct{ path, slug string }{
		{"/promises?orderId=o&after=x", "unknown-query-parameter"},
		{"/promise-books?nodeId=N&date=2026-10-22&offset=1", "unknown-query-parameter"},
		{"/promises?orderId=o&limit=0", "invalid-limit"},
		{"/promises?orderId=o&limit=501", "invalid-limit"},
		{"/promises?orderId=o&limit=", "invalid-limit"},
		{"/promises?orderId=o&limit=1&limit=2", "invalid-limit"},
		{"/promises?orderId=o&cursor=!!!", "invalid-cursor"},
		{"/promises?orderId=o&cursor=a&cursor=b", "invalid-cursor"},
		{"/promise-books?nodeId=N&date=2026-10-22&cursor=bm90anNvbg", "invalid-cursor"},
	}
	for _, c := range cases {
		rec := do(h, "GET", c.path, ``)
		var p struct{ Type string }
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		if rec.Code != 400 || p.Type != problemBaseURI+c.slug {
			t.Errorf("%s = %d %s, want 400 %s", c.path, rec.Code, p.Type, c.slug)
		}
	}
}
