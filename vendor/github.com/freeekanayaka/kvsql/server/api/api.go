package api

import (
	"net/http"

	"github.com/freeekanayaka/kvsql/server/membership"
)

func New(localNodeAddress string, membership *membership.Membership) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/dqlite", dqliteHandleFunc(localNodeAddress))
	mux.Handle("/cluster", clusterHandler(membership))
	return mux
}
