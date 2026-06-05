package utils

import (
	"net/http"
	"strconv"
)

type Pagination struct {
	Page  int
	Limit int
}

func ParsePagination(r *http.Request) Pagination {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return Pagination{Page: page, Limit: limit}
}

func (p Pagination) Offset() int {
	return (p.Page - 1) * p.Limit
}
